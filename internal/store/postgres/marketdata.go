package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultPageSize = 20

// MarketDataStore implements marketdata.Store using PostgreSQL.
type MarketDataStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ marketdata.Store = (*MarketDataStore)(nil)

func NewMarketDataStore(pool *pgxpool.Pool) *MarketDataStore {
	return &MarketDataStore{pool: pool}
}

// assetColumns is the canonical column list scanned by scanAsset.
const assetColumns = "id, symbol, name, type, market, quote, tags, created_at, updated_at, " +
	"identity_verdict, identity_score, identity_signals, verdict_source, verdict_set_at"

// prefixedAssetColumns is assetColumns for queries that join assets as "a".
const prefixedAssetColumns = "a.id, a.symbol, a.name, a.type, a.market, a.quote, a.tags, " +
	"a.created_at, a.updated_at, a.identity_verdict, a.identity_score, a.identity_signals, " +
	"a.verdict_source, a.verdict_set_at"

type rowScanner interface {
	Scan(dest ...any) error
}

// scanAsset scans one assetColumns row. Callers map pgx.ErrNoRows themselves.
func scanAsset(row rowScanner) (*entity.Asset, error) {
	var asset entity.Asset
	var typeStr string
	var quote *string
	var tagsJSON []byte
	var signalsJSON []byte
	var verdictSource *string

	if err := row.Scan(
		&asset.ID,
		&asset.Symbol,
		&asset.Name,
		&typeStr,
		&asset.Market,
		&quote,
		&tagsJSON,
		&asset.CreatedAt,
		&asset.UpdatedAt,
		&asset.IdentityVerdict,
		&asset.IdentityScore,
		&signalsJSON,
		&verdictSource,
		&asset.VerdictSetAt,
	); err != nil {
		return nil, err
	}

	asset.Type = stringToAssetType(typeStr)
	if quote != nil {
		asset.Quote = *quote
	}
	if verdictSource != nil {
		asset.VerdictSource = *verdictSource
	}
	if err := json.Unmarshal(tagsJSON, &asset.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
	}
	if len(signalsJSON) > 0 {
		if err := json.Unmarshal(signalsJSON, &asset.IdentitySignals); err != nil {
			return nil, fmt.Errorf("failed to unmarshal identity signals: %w", err)
		}
	}
	return &asset, nil
}

// nullIfEmpty maps "" to SQL NULL so unique constraints ignore absent values.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// provenanceValue writes an unstated provenance as NULL rather than as an empty
// string. "The source said nothing" and "the source said empty" would be the same
// row otherwise, and the first is the honest reading of every row written before
// the column existed.
func provenanceValue(p entity.PriceProvenance) *string {
	if p == entity.PriceProvenanceUnknown {
		return nil
	}
	s := string(p)
	return &s
}

// readProvenance maps a NULL provenance back to the unknown value. Reading NULL
// as "traded" would be the system inventing a claim the source never made.
func readProvenance(s *string) entity.PriceProvenance {
	if s == nil {
		return entity.PriceProvenanceUnknown
	}
	return entity.PriceProvenance(*s)
}

// CreateAsset creates a new asset in the database.
func (s *MarketDataStore) CreateAsset(ctx context.Context, asset *entity.Asset) (*entity.Asset, error) {
	if asset == nil {
		return nil, fmt.Errorf("%w: asset is required", store.ErrInvalidArgument)
	}
	if asset.Name == "" {
		return nil, fmt.Errorf("%w: asset name is required", store.ErrInvalidArgument)
	}
	if asset.Type == entity.AssetTypeUnspecified {
		return nil, fmt.Errorf("%w: asset type is required", store.ErrInvalidArgument)
	}

	asset.Symbol = entity.NormalizeSymbol(asset.Symbol)
	asset.Market = entity.NormalizeMarket(asset.Market)
	if asset.Market == "" {
		asset.Market = entity.DefaultMarket(asset.Type)
	}
	if asset.Market == "" {
		return nil, fmt.Errorf("%w: asset market is required for type %s", store.ErrInvalidArgument, assetTypeToString(asset.Type))
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	asset.ID = id.String()

	tagsJSON, err := json.Marshal(asset.Tags)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tags: %w", err)
	}

	query := `
		INSERT INTO assets (id, symbol, name, type, market, quote, tags, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		asset.ID,
		asset.Symbol,
		asset.Name,
		assetTypeToString(asset.Type),
		asset.Market,
		nullIfEmpty(asset.Quote),
		tagsJSON,
	).Scan(&asset.CreatedAt, &asset.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create asset: %w", err)
	}

	return asset, nil
}

// GetAsset retrieves an asset by ID.
func (s *MarketDataStore) GetAsset(ctx context.Context, id string) (*entity.Asset, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: asset ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT ` + assetColumns + `
		FROM assets
		WHERE id = $1`

	asset, err := scanAsset(s.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: asset with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get asset: %w", err)
	}

	return asset, nil
}

// GetOrCreateAssetBySymbol returns the asset a caller means by a bare ticker of
// a known kind, creating it when it does not exist yet. nameIfNew is used only
// when creating. Safe under concurrent inserts: if a concurrent write wins the
// race, the existing row is returned.
//
// The lookup is the FULL composite identity (symbol, market, type), with the
// market implied by the type exactly as CreateAsset implies it. That is not a
// refinement — it is the write path read back correctly. Anything narrower asks
// a wider question than the one that was written, and every extra row sharing
// the ticker then answers "ambiguous" instead of naming a row.
//
// Dev 2026-08-14 is the proof of what the wider question costs. Correcting
// Binance's quote currency from forex to cryptocurrency (commit 0f5c100) left
// the old USDT row in place, and the correction minted a twin instead of
// updating the row. Every sweep afterwards threw away Binance's whole batch on
// "symbol USDT is ambiguous" — for two months, unnoticed until CoinGecko's quota
// ran out and there was no second source left to notice it with. A quote
// currency resolved by an ambiguous ticker is a base that can silently become a
// different base.
//
// This asks for an exact row and creates that exact row when it is missing, so
// there is no question left for a second row to make ambiguous. It REPLACES an
// earlier scoping by type alone, which was kept because legacy rows had been
// backfilled to 'crypto' regardless of type: USD sat at (USD, crypto, forex)
// while a fresh one would be created at (USD, forex, forex), so matching on
// market would have missed the row every stored price points at. That objection
// is settled by moving USD onto the forex market rather than by widening the
// lookup — a currency on the crypto market both reads as a fake token and
// collides with impostors minted by whoever pays the gas.
func (s *MarketDataStore) GetOrCreateAssetBySymbol(ctx context.Context, symbol, nameIfNew string, typ entity.AssetType) (*entity.Asset, error) {
	market := entity.DefaultMarket(typ)
	if market == "" {
		return nil, fmt.Errorf("%w: asset type %v has no default market to resolve %s in", store.ErrInvalidArgument, typ, symbol)
	}

	a, err := s.FindAssetByIdentity(ctx, symbol, market, typ)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	created, err := s.CreateAsset(ctx, &entity.Asset{
		Symbol: symbol,
		Name:   nameIfNew,
		Type:   typ,
		Tags:   []string{},
	})
	if err != nil {
		// Concurrent insert: another process created it first — read back.
		if errors.Is(err, store.ErrConstraint) {
			return s.FindAssetByIdentity(ctx, symbol, market, typ)
		}
		return nil, err
	}
	return created, nil
}

// GetAssetBySymbol returns an asset by its symbol. The symbol is normalized
// (trim + uppercase) so lookups are case-insensitive. Identity is composite
// (symbol, market, type): a symbol-only lookup succeeds only while the symbol
// is unambiguous; with matches on several markets it fails explicitly rather
// than silently picking one.
//
// Assets isolated on their own contract market do not take part in that
// ambiguity. A contract is minted by whoever pays the gas, so letting one
// contend for a ticker hands anybody a switch for every lookup that resolves by
// symbol — and the quote currency is resolved on every valuation. Dev
// 2026-08-04 is the proof: syncing a whale wallet pulled in "US Dollar
// Shitcoin" (base) and a counterfeit "Tether" (bsc), both calling themselves
// USD, and the portfolio total went to an error — $0 on screen — for the whole
// instance. The isolation that keeps such a token out of the sum
// (marketForContract) has to keep it out of the name resolution too.
//
// The contract market is not ignored, only outranked: a symbol that ONLY an
// isolated contract carries still resolves, and two of them still collide.
func (s *MarketDataStore) GetAssetBySymbol(ctx context.Context, symbol string) (*entity.Asset, error) {
	return s.findAssetBySymbol(ctx, symbol, entity.AssetTypeUnspecified)
}

// findAssetBySymbol is GetAssetBySymbol with an optional type filter;
// entity.AssetTypeUnspecified means "any kind", which is the symbol-only
// question GetAssetBySymbol asks. The tiering is shared rather than copied:
// which rows may contend for a ticker is one rule, and a second copy of it is
// how the contract market would start shadowing a ticker again in one caller
// and not the other.
func (s *MarketDataStore) findAssetBySymbol(ctx context.Context, symbol string, typ entity.AssetType) (*entity.Asset, error) {
	symbol = entity.NormalizeSymbol(symbol)
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is required", store.ErrInvalidArgument)
	}

	args := []any{symbol}
	typeFilter := ""
	if typ != entity.AssetTypeUnspecified {
		args = append(args, assetTypeToString(typ))
		typeFilter = fmt.Sprintf(" AND type = $%d", len(args))
	}

	// Ordered so the preferred tier comes first; the cap is a sanity bound, the
	// tiers are counted in Go.
	query := `
		SELECT ` + assetColumns + `
		FROM assets
		WHERE symbol = $1` + typeFilter + `
		ORDER BY (market LIKE 'onchain:%') ASC, created_at ASC
		LIMIT 50`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get asset by symbol: %w", err)
	}
	defer rows.Close()

	var named, isolated []*entity.Asset
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan asset: %w", err)
		}
		if strings.HasPrefix(asset.Market, onchainMarketPrefix) {
			isolated = append(isolated, asset)
			continue
		}
		named = append(named, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to get asset by symbol: %w", err)
	}

	candidates := named
	if len(candidates) == 0 {
		candidates = isolated
	}
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("%w: asset with symbol %s", store.ErrNotFound, symbol)
	case 1:
		return candidates[0], nil
	default:
		return nil, fmt.Errorf("%w: symbol %s is ambiguous across markets, look up by ID or specify market", store.ErrInvalidArgument, symbol)
	}
}

// onchainMarketPrefix marks a market minted from a contract address rather than
// assigned by an authority. See entity.ContractMarket.
const onchainMarketPrefix = "onchain:"

// FindAssetByIdentity looks up an asset by its exact composite identity
// (symbol, market, type). Inputs are normalized the same way CreateAsset
// normalizes them, so a find-then-create sequence targets the same row.
func (s *MarketDataStore) FindAssetByIdentity(ctx context.Context, symbol, market string, typ entity.AssetType) (*entity.Asset, error) {
	symbol = entity.NormalizeSymbol(symbol)
	market = entity.NormalizeMarket(market)
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is required", store.ErrInvalidArgument)
	}
	if market == "" {
		return nil, fmt.Errorf("%w: market is required", store.ErrInvalidArgument)
	}
	if typ == entity.AssetTypeUnspecified {
		return nil, fmt.Errorf("%w: asset type is required", store.ErrInvalidArgument)
	}

	query := `
		SELECT ` + assetColumns + `
		FROM assets
		WHERE symbol = $1 AND market = $2 AND type = $3`

	asset, err := scanAsset(s.pool.QueryRow(ctx, query, symbol, market, assetTypeToString(typ)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: asset %s on market %s", store.ErrNotFound, symbol, market)
		}
		return nil, fmt.Errorf("failed to find asset by identity: %w", err)
	}

	return asset, nil
}

// UpdateAsset updates an asset with the specified fields.
func (s *MarketDataStore) UpdateAsset(ctx context.Context, asset *entity.Asset, fields []string) (*entity.Asset, error) {
	if asset == nil || asset.ID == "" {
		return nil, fmt.Errorf("%w: asset with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(asset.ID) {
		return nil, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{asset.ID}
	argIdx := 2

	for _, field := range fields {
		switch field {
		case "symbol":
			setClauses = append(setClauses, fmt.Sprintf("symbol = $%d", argIdx))
			args = append(args, entity.NormalizeSymbol(asset.Symbol))
			argIdx++
		case "name":
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, asset.Name)
			argIdx++
		case "type":
			setClauses = append(setClauses, fmt.Sprintf("type = $%d", argIdx))
			args = append(args, assetTypeToString(asset.Type))
			argIdx++
		case "market":
			market := entity.NormalizeMarket(asset.Market)
			if market == "" {
				return nil, fmt.Errorf("%w: market cannot be empty", store.ErrInvalidArgument)
			}
			setClauses = append(setClauses, fmt.Sprintf("market = $%d", argIdx))
			args = append(args, market)
			argIdx++
		case "quote":
			setClauses = append(setClauses, fmt.Sprintf("quote = $%d", argIdx))
			args = append(args, nullIfEmpty(asset.Quote))
			argIdx++
		case "tags":
			tagsJSON, err := json.Marshal(asset.Tags)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tags: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("tags = $%d", argIdx))
			args = append(args, tagsJSON)
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE assets
		SET %s
		WHERE id = $1
		RETURNING `+assetColumns,
		strings.Join(setClauses, ", "))

	result, err := scanAsset(s.pool.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: asset with ID %s", store.ErrNotFound, asset.ID)
		}
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to update asset: %w", err)
	}

	return result, nil
}

// SetAssetVerdict writes an identity verdict (scam-filtering axis 1) with its
// score, signals and provenance. A user verdict (source "user:*") is terminal:
// an automated write never overwrites one, while a user write always wins. The
// bool reports whether the row was actually written, so a rescoring pass can
// count what it changed versus what it left to a human decision.
func (s *MarketDataStore) SetAssetVerdict(ctx context.Context, assetID, verdict string, score *float64, signals map[string]float64, source string) (bool, error) {
	if !isValidUUID(assetID) {
		return false, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}
	if verdict == "" || source == "" {
		return false, fmt.Errorf("%w: verdict and source are required", store.ErrInvalidArgument)
	}

	var signalsJSON []byte
	if signals != nil {
		var err error
		if signalsJSON, err = json.Marshal(signals); err != nil {
			return false, fmt.Errorf("failed to marshal identity signals: %w", err)
		}
	}

	// The guard lets a user source through unconditionally and blocks any other
	// source from clobbering an existing user verdict.
	const query = `
		UPDATE assets
		SET identity_verdict = $2, identity_score = $3, identity_signals = $4,
		    verdict_source = $5, verdict_set_at = NOW(), updated_at = NOW()
		WHERE id = $1
		  AND ($5 LIKE 'user:%' OR verdict_source IS NULL OR verdict_source NOT LIKE 'user:%')`

	tag, err := s.pool.Exec(ctx, query, assetID, verdict, score, signalsJSON, source)
	if err != nil {
		return false, fmt.Errorf("failed to set asset verdict: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FindAssetIDByExternalRef resolves an asset by its identifier in an external
// namespace (a contract on a chain, a provider coin id). This is the
// contract-identity lookup the sync path uses so a scam clone of a real ticker
// resolves to its own asset, not the real one. Returns ErrNotFound when the ref
// is unmapped.
func (s *MarketDataStore) FindAssetIDByExternalRef(ctx context.Context, source, ref string) (string, error) {
	if source == "" || ref == "" {
		return "", fmt.Errorf("%w: source and ref are required", store.ErrInvalidArgument)
	}
	var assetID string
	err := s.pool.QueryRow(ctx,
		`SELECT asset_id FROM asset_external_refs WHERE source = $1 AND ref = $2`,
		source, ref,
	).Scan(&assetID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: external ref %s/%s", store.ErrNotFound, source, ref)
		}
		return "", fmt.Errorf("failed to find asset by external ref: %w", err)
	}
	return assetID, nil
}

// CreateAssetExternalRef maps an asset to an external identifier. A conflicting
// (source, ref) is left untouched — identity is stable once bound, and a manual
// link must never be silently replaced by an auto one. ErrConstraint signals the
// caller a mapping already exists.
func (s *MarketDataStore) CreateAssetExternalRef(ctx context.Context, ref *entity.AssetExternalRef) (*entity.AssetExternalRef, error) {
	if ref == nil || ref.AssetID == "" || ref.Source == "" || ref.Ref == "" {
		return nil, fmt.Errorf("%w: asset_id, source and ref are required", store.ErrInvalidArgument)
	}
	if !isValidUUID(ref.AssetID) {
		return nil, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}
	if ref.Origin == "" {
		ref.Origin = entity.RefOriginAuto
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	ref.ID = id.String()

	err = s.pool.QueryRow(ctx,
		`INSERT INTO asset_external_refs (id, asset_id, source, ref, origin, created_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 RETURNING created_at`,
		ref.ID, ref.AssetID, ref.Source, ref.Ref, ref.Origin,
	).Scan(&ref.CreatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create asset external ref: %w", err)
	}
	return ref, nil
}

// DeleteAssetExternalRef removes one binding, refusing to touch a ref that
// belongs to a different asset.
//
// The asset id is part of the WHERE clause rather than checked beforehand: a
// read-then-delete would let a concurrent rebind slip between the two, and the
// whole point of this call is that identity does not move by accident.
// ErrNotFound covers both "no such ref" and "not this asset's ref" — the caller
// is repairing one known binding, and telling the two apart would only confirm
// the existence of somebody else's row.
func (s *MarketDataStore) DeleteAssetExternalRef(ctx context.Context, assetID, id string) error {
	if !isValidUUID(assetID) {
		return fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid external ref ID format", store.ErrInvalidArgument)
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM asset_external_refs
		WHERE id = $1 AND asset_id = $2`, id, assetID)
	if err != nil {
		return fmt.Errorf("failed to delete asset external ref: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: external ref %s on asset %s", store.ErrNotFound, id, assetID)
	}
	return nil
}

// ListAssetExternalRefs returns the external refs of several assets at once.
// The pricing path needs the reverse of FindAssetIDByExternalRef — which chain
// a contract lives on — for every asset in a sweep, and asking per asset would
// be hundreds of round trips.
func (s *MarketDataStore) ListAssetExternalRefs(ctx context.Context, assetIDs []string) ([]*entity.AssetExternalRef, error) {
	if len(assetIDs) == 0 {
		return nil, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, asset_id, source, ref, origin, created_at
		FROM asset_external_refs
		WHERE asset_id = ANY($1::uuid[])
		ORDER BY asset_id, created_at`, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list asset external refs: %w", err)
	}
	defer rows.Close()

	var out []*entity.AssetExternalRef
	for rows.Next() {
		var ref entity.AssetExternalRef
		if err := rows.Scan(&ref.ID, &ref.AssetID, &ref.Source, &ref.Ref, &ref.Origin, &ref.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan asset external ref: %w", err)
		}
		out = append(out, &ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate asset external refs: %w", err)
	}
	return out, nil
}

// CreateAssetRiskFlag records a situational risk on an asset (risk-model axis
// 2). ReviewAt is required by the caller, not by the column: the schema allows
// NULL because rows predate the rule, while nothing written from here may hang
// without a date to revisit it.
//
// Flags accumulate rather than replace. Two exploits a year apart are two
// events, and collapsing them would erase the first one's history the moment
// the second is filed.
func (s *MarketDataStore) CreateAssetRiskFlag(ctx context.Context, flag *entity.AssetRiskFlag) (*entity.AssetRiskFlag, error) {
	if flag == nil || flag.AssetID == "" || flag.Kind == "" {
		return nil, fmt.Errorf("%w: asset_id and kind are required", store.ErrInvalidArgument)
	}
	if !isValidUUID(flag.AssetID) {
		return nil, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}
	if flag.ReviewAt == nil {
		return nil, fmt.Errorf("%w: review_at is required", store.ErrInvalidArgument)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	flag.ID = id.String()

	err = s.pool.QueryRow(ctx,
		`INSERT INTO asset_risk_flags (id, asset_id, kind, note, action_hint, review_at, set_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		 RETURNING created_at`,
		flag.ID, flag.AssetID, flag.Kind,
		nullIfEmpty(flag.Note), nullIfEmpty(flag.ActionHint), flag.ReviewAt, nullIfEmpty(flag.SetBy),
	).Scan(&flag.CreatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create asset risk flag: %w", err)
	}
	return flag, nil
}

// ListAssetRiskFlags returns one asset's risk flags, newest first: the card
// shows the current situation, and the oldest flag is the least likely to still
// describe it.
//
// Single-asset by design. Unlike external refs, which the pricing sweep needs
// for every asset it touches, flags have exactly one consumer — the asset card
// — and batching them would invite the catalogue list to carry them too.
func (s *MarketDataStore) ListAssetRiskFlags(ctx context.Context, assetID string) ([]*entity.AssetRiskFlag, error) {
	if !isValidUUID(assetID) {
		return nil, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, asset_id, kind, COALESCE(note, ''), COALESCE(action_hint, ''),
		       review_at, COALESCE(set_by, ''), created_at
		FROM asset_risk_flags
		WHERE asset_id = $1
		ORDER BY created_at DESC`, assetID)
	if err != nil {
		return nil, fmt.Errorf("failed to list asset risk flags: %w", err)
	}
	defer rows.Close()

	var out []*entity.AssetRiskFlag
	for rows.Next() {
		var flag entity.AssetRiskFlag
		if err := rows.Scan(&flag.ID, &flag.AssetID, &flag.Kind, &flag.Note, &flag.ActionHint,
			&flag.ReviewAt, &flag.SetBy, &flag.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan asset risk flag: %w", err)
		}
		out = append(out, &flag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate asset risk flags: %w", err)
	}
	return out, nil
}

// DeleteAssetRiskFlag removes one flag, refusing one that belongs to a
// different asset — the asset id is in the WHERE clause for the same reason it
// is in DeleteAssetExternalRef: a mistyped id must not reach somebody else's
// row, and ErrNotFound deliberately does not distinguish "no such flag" from
// "not this asset's flag".
func (s *MarketDataStore) DeleteAssetRiskFlag(ctx context.Context, assetID, id string) error {
	if !isValidUUID(assetID) {
		return fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid risk flag ID format", store.ErrInvalidArgument)
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM asset_risk_flags
		WHERE id = $1 AND asset_id = $2`, id, assetID)
	if err != nil {
		return fmt.Errorf("failed to delete asset risk flag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: risk flag %s on asset %s", store.ErrNotFound, id, assetID)
	}
	return nil
}

// FindTickerIncumbent reports whether this asset's ticker is already held, on
// one of its own chains, by an older asset bound to a different contract.
//
// The shape it looks for is the one thing a lookalike cannot fake away: a chain
// cannot carry two contracts of the same asset. Symbol, name and amount are all
// copyable, so "two assets share a ticker" says nothing on its own — cross-chain
// instances and catalogue duplicates share tickers constantly. What does not
// happen by accident is a second contract on the SAME chain claiming a ticker
// that an established asset already binds there.
//
// Seniority is deliberate and asymmetric, so the newcomer is judged and the
// incumbent is not: the incumbent must be older AND carry a price history, which
// is the catalogue's cheapest proof that the outside world agrees this ticker
// belongs to it. Without the price condition, two unlisted duplicates would
// condemn each other; without the age condition, the real asset would be
// condemned by its own impostor on a rescore.
//
// Known false positive, accepted: LP tokens (UNI-V2, SLP, CAKE-LP) give every
// pool the same ticker on one chain by construction, so a listed pool makes the
// next pool look like an impostor.
func (s *MarketDataStore) FindTickerIncumbent(ctx context.Context, assetID string) (string, error) {
	if assetID == "" {
		return "", fmt.Errorf("%w: asset ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(assetID) {
		return "", fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	var incumbentID string
	err := s.pool.QueryRow(ctx, `
		SELECT inc.id
		FROM assets claim
		JOIN asset_external_refs cr
		  ON cr.asset_id = claim.id AND cr.source LIKE 'onchain:%'
		JOIN asset_external_refs ir
		  ON ir.source = cr.source AND lower(ir.ref) <> lower(cr.ref)
		JOIN assets inc
		  ON inc.id = ir.asset_id
		WHERE claim.id = $1
		  AND inc.id <> claim.id
		  AND upper(inc.symbol) = upper(claim.symbol)
		  AND inc.created_at < claim.created_at
		  AND EXISTS (SELECT 1 FROM prices p WHERE p.asset_id = inc.id)
		ORDER BY inc.created_at
		LIMIT 1`, assetID).Scan(&incumbentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: no ticker incumbent for asset %s", store.ErrNotFound, assetID)
		}
		return "", fmt.Errorf("failed to find ticker incumbent: %w", err)
	}
	return incumbentID, nil
}

// DeleteAsset deletes an asset by ID.
func (s *MarketDataStore) DeleteAsset(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: asset ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM assets WHERE id = $1", id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: cannot delete asset due to existing dependencies", store.ErrConstraint)
		}
		return fmt.Errorf("failed to delete asset: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: asset with ID %s", store.ErrNotFound, id)
	}

	return nil
}

// ListAssets returns a paginated list of assets, optionally filtered by tags.
func (s *MarketDataStore) ListAssets(ctx context.Context, opts marketdata.ListAssetsOpts) ([]*entity.Asset, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("id > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	if len(opts.Tags) > 0 {
		tagsJSON, err := json.Marshal(opts.Tags)
		if err != nil {
			return nil, "", fmt.Errorf("failed to marshal tags filter: %w", err)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("tags @> $%d::jsonb", argIdx))
		args = append(args, string(tagsJSON))
		argIdx++
	}

	if opts.IdentityVerdict != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("identity_verdict = $%d", argIdx))
		args = append(args, opts.IdentityVerdict)
		argIdx++
	}

	if len(opts.IDs) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("id = ANY($%d::uuid[])", argIdx))
		args = append(args, opts.IDs)
		argIdx++
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT `+assetColumns+`
		FROM assets
		%s
		ORDER BY id
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list assets: %w", err)
	}
	defer rows.Close()

	assets := make([]*entity.Asset, 0, limit)
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, "", fmt.Errorf("failed to scan asset: %w", err)
		}
		assets = append(assets, asset)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate assets: %w", err)
	}

	var nextPageToken string
	if len(assets) > limit {
		lastAsset := assets[limit-1]
		assets = assets[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastAsset.ID))
	}

	return assets, nextPageToken, nil
}

// ListStalePricingTargets returns assets whose next price attempt for this
// source is due, oldest deadline first.
//
// The join is against price_fetch_attempts rather than prices because an asset
// the provider does not list never gets a price row: ordering on price age
// would park it at the head of the queue permanently and spend every sweep's
// budget re-asking for it. NULLS FIRST puts never-attempted assets ahead of
// everything else, so a freshly synced token is priced on the next sweep, and
// the id tie-break keeps the rotation deterministic.
func (s *MarketDataStore) ListStalePricingTargets(ctx context.Context, opts marketdata.StalePricingOpts) ([]*entity.Asset, error) {
	if opts.SourceID == "" {
		return nil, fmt.Errorf("%w: source_id is required", store.ErrInvalidArgument)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	args := []any{opts.SourceID, now}
	argIdx := 3
	whereClauses := []string{"(f.next_attempt_at IS NULL OR f.next_attempt_at <= $2)"}

	if len(opts.Symbols) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("a.symbol = ANY($%d)", argIdx))
		args = append(args, opts.Symbols)
		argIdx++
	}
	if len(opts.ExcludeSymbols) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("a.symbol <> ALL($%d)", argIdx))
		args = append(args, opts.ExcludeSymbols)
		argIdx++
	}
	if len(opts.ExcludeVerdicts) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("a.identity_verdict <> ALL($%d)", argIdx))
		args = append(args, opts.ExcludeVerdicts)
		argIdx++
	}

	limitClause := ""
	if opts.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
	}

	query := fmt.Sprintf(`
		SELECT `+prefixedAssetColumns+`
		FROM assets a
		LEFT JOIN price_fetch_attempts f
			ON f.asset_id = a.id AND f.source_id = $1
		WHERE %s
		ORDER BY f.next_attempt_at ASC NULLS FIRST, a.id
		%s`,
		strings.Join(whereClauses, " AND "), limitClause)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list stale pricing targets: %w", err)
	}
	defer rows.Close()

	var assets []*entity.Asset
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan asset: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate stale pricing targets: %w", err)
	}

	return assets, nil
}

// SweepSchedule aggregates the attempt log per source into the queue the next
// sweep would find.
//
// Two queries rather than one. The counts of due/deferred come from
// price_fetch_attempts grouped by source, which is cheap and indexed. The
// never-attempted count cannot: an asset with no row for this source produces
// no row to group, so it has to be counted against the asset table with the
// same verdict filter the selection applies. Folding both into one statement
// would mean a cross join of every asset with every source, which is the
// catalogue times four for a number that fits in a uint32.
func (s *MarketDataStore) SweepSchedule(ctx context.Context, opts marketdata.SweepScheduleOpts) ([]*entity.SourceSchedule, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	args := []any{now}
	where := []string{"TRUE"}
	if len(opts.SourceIDs) > 0 {
		where = append(where, fmt.Sprintf("f.source_id = ANY($%d)", len(args)+1))
		args = append(args, opts.SourceIDs)
	}
	if len(opts.ExcludeVerdicts) > 0 {
		where = append(where, fmt.Sprintf("a.identity_verdict <> ALL($%d)", len(args)+1))
		args = append(args, opts.ExcludeVerdicts)
	}

	query := fmt.Sprintf(`
		SELECT f.source_id,
		       count(*) FILTER (WHERE f.next_attempt_at <= $1)                AS due_now,
		       count(*) FILTER (WHERE f.next_attempt_at > $1)                 AS deferred,
		       min(f.next_attempt_at) FILTER (WHERE f.next_attempt_at > $1)   AS soonest_due,
		       max(f.next_attempt_at) FILTER (WHERE f.next_attempt_at > $1)   AS latest_deferred,
		       coalesce(max(f.misses), 0)                                     AS max_misses
		FROM price_fetch_attempts f
		JOIN assets a ON a.id = f.asset_id
		WHERE %s
		GROUP BY f.source_id
		ORDER BY f.source_id`, strings.Join(where, " AND "))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to read sweep schedule: %w", err)
	}
	defer rows.Close()

	bySource := map[string]*entity.SourceSchedule{}
	var out []*entity.SourceSchedule
	for rows.Next() {
		var sched entity.SourceSchedule
		var due, deferred, misses int64
		var soonest, latest *time.Time
		if err := rows.Scan(&sched.SourceID, &due, &deferred, &soonest, &latest, &misses); err != nil {
			return nil, fmt.Errorf("failed to scan sweep schedule: %w", err)
		}
		sched.DueNow = uint32(due)        // #nosec G115 -- bounded by the catalogue
		sched.Deferred = uint32(deferred) // #nosec G115 -- bounded by the catalogue
		sched.MaxMisses = uint32(misses)  // #nosec G115 -- bounded by the back-off cap
		if soonest != nil {
			sched.SoonestDue = *soonest
		}
		if latest != nil {
			sched.LatestDeferred = *latest
		}
		row := sched
		bySource[sched.SourceID] = &row
		out = append(out, &row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sweep schedule: %w", err)
	}

	if len(opts.SourceIDs) == 0 {
		return out, nil
	}

	// Never-attempted is only answerable for sources we were asked about by
	// name: without a list there is nothing to count an absent row against.
	// A source with no attempt rows at all still gets an entry — "this source
	// has never been asked" is the most important thing this RPC can say, and
	// omitting it would report silence as absence.
	for _, sourceID := range opts.SourceIDs {
		n, err := s.countNeverAttempted(ctx, sourceID, opts.ExcludeVerdicts)
		if err != nil {
			return nil, err
		}
		sched, ok := bySource[sourceID]
		if !ok {
			sched = &entity.SourceSchedule{SourceID: sourceID}
			out = append(out, sched)
		}
		sched.NeverAttempted = n
	}

	return out, nil
}

// countNeverAttempted counts assets this source has no attempt row for. They
// sort ahead of everything else in the selection, so this is the head of the
// queue rather than its tail.
func (s *MarketDataStore) countNeverAttempted(ctx context.Context, sourceID string, excludeVerdicts []string) (uint32, error) {
	args := []any{sourceID}
	where := []string{"f.asset_id IS NULL"}
	if len(excludeVerdicts) > 0 {
		where = append(where, fmt.Sprintf("a.identity_verdict <> ALL($%d)", len(args)+1))
		args = append(args, excludeVerdicts)
	}

	query := fmt.Sprintf(`
		SELECT count(*)
		FROM assets a
		LEFT JOIN price_fetch_attempts f
			ON f.asset_id = a.id AND f.source_id = $1
		WHERE %s`, strings.Join(where, " AND "))

	var n int64
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count never-attempted assets: %w", err)
	}
	return uint32(n), nil // #nosec G115 -- bounded by the catalogue
}

// RecordPriceAttempts records one sweep's outcome for a source.
//
// A hit resets the miss counter and schedules the next attempt one TTL out. A
// miss doubles the interval per consecutive miss up to MaxBackoff, which is
// what drains permanently unlistable assets out of the rotation without ever
// dropping them: a token listed later is still retried, just rarely.
func (s *MarketDataStore) RecordPriceAttempts(ctx context.Context, opts marketdata.RecordAttemptsOpts) error {
	if opts.SourceID == "" {
		return fmt.Errorf("%w: source_id is required", store.ErrInvalidArgument)
	}
	if len(opts.Priced) == 0 && len(opts.Missed) == 0 {
		return nil
	}
	at := opts.At
	if at.IsZero() {
		at = time.Now()
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	maxBackoff := opts.MaxBackoff
	if maxBackoff < ttl {
		maxBackoff = ttl
	}

	batch := &pgx.Batch{}
	if len(opts.Priced) > 0 {
		batch.Queue(`
			INSERT INTO price_fetch_attempts
				(asset_id, source_id, attempted_at, succeeded_at, misses, next_attempt_at)
			SELECT id, $2, $3::timestamptz, $3::timestamptz, 0,
			       $3::timestamptz + make_interval(secs => $4::float8)
			FROM unnest($1::uuid[]) AS id
			ON CONFLICT (asset_id, source_id) DO UPDATE SET
				attempted_at    = EXCLUDED.attempted_at,
				succeeded_at    = EXCLUDED.succeeded_at,
				misses          = 0,
				next_attempt_at = EXCLUDED.next_attempt_at`,
			opts.Priced, opts.SourceID, at, ttl.Seconds())
	}
	if len(opts.Missed) > 0 {
		// power(2, misses) uses the pre-update value, so the first miss waits
		// one TTL, the second two, and so on until the cap.
		batch.Queue(`
			INSERT INTO price_fetch_attempts
				(asset_id, source_id, attempted_at, succeeded_at, misses, next_attempt_at)
			SELECT id, $2, $3::timestamptz, NULL, 1,
			       $3::timestamptz + make_interval(secs => $4::float8)
			FROM unnest($1::uuid[]) AS id
			ON CONFLICT (asset_id, source_id) DO UPDATE SET
				attempted_at    = EXCLUDED.attempted_at,
				misses          = price_fetch_attempts.misses + 1,
				next_attempt_at = EXCLUDED.attempted_at + make_interval(
					secs => LEAST($4::float8 * power(2, price_fetch_attempts.misses), $5::float8))`,
			opts.Missed, opts.SourceID, at, ttl.Seconds(), maxBackoff.Seconds())
	}

	results := s.pool.SendBatch(ctx, batch)
	defer func() { _ = results.Close() }()
	for range batch.Len() {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("failed to record price attempts: %w", err)
		}
	}
	return nil
}

// ResetPriceAttempts forgives the back-off accrued against one source.
//
// The miss counter is the schedule's whole memory: next_attempt_at is derived
// from it, so zeroing the counter without also moving the deadline would leave
// the assets deferred until a date computed from a history just disclaimed.
// Both move together, and the attempt timestamps are deliberately left alone —
// what was asked and when is a fact; only the conclusion drawn from it is
// being withdrawn.
func (s *MarketDataStore) ResetPriceAttempts(ctx context.Context, sourceID string, at time.Time) (int64, error) {
	if sourceID == "" {
		return 0, fmt.Errorf("%w: source_id is required", store.ErrInvalidArgument)
	}
	if at.IsZero() {
		at = time.Now()
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE price_fetch_attempts
		SET misses = 0, next_attempt_at = $2::timestamptz
		WHERE source_id = $1 AND (misses > 0 OR next_attempt_at > $2::timestamptz)`,
		sourceID, at)
	if err != nil {
		return 0, fmt.Errorf("failed to reset price attempts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CreatePrice creates a new price record.
func (s *MarketDataStore) CreatePrice(ctx context.Context, price *entity.StoredPrice) (*entity.StoredPrice, error) {
	if price == nil {
		return nil, fmt.Errorf("%w: price is required", store.ErrInvalidArgument)
	}
	if price.AssetID == "" || price.BaseAssetID == "" || price.SourceID == "" {
		return nil, fmt.Errorf("%w: asset_id, base_asset_id, and source_id are required", store.ErrInvalidArgument)
	}
	if !isValidUUID(price.AssetID) {
		return nil, fmt.Errorf("%w: invalid asset_id format", store.ErrInvalidArgument)
	}
	if !isValidUUID(price.BaseAssetID) {
		return nil, fmt.Errorf("%w: invalid base_asset_id format", store.ErrInvalidArgument)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	price.ID = id.String()

	if price.Timestamp.IsZero() {
		price.Timestamp = time.Now()
	}

	query := `
		INSERT INTO prices (id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp, provenance)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING timestamp`

	err = s.pool.QueryRow(ctx, query,
		price.ID,
		price.SourceID,
		price.AssetID,
		price.BaseAssetID,
		price.Interval,
		price.Decimals,
		price.Last,
		price.Open,
		price.High,
		price.Low,
		price.Close,
		price.Volume,
		price.MarketCap,
		price.Timestamp,
		provenanceValue(price.Provenance),
	).Scan(&price.Timestamp)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: price constraint failed: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create price: %w", err)
	}

	return price, nil
}

// CreatePrices creates multiple prices in bulk.
// Individual failures are counted and returned as a combined error so callers
// can surface partial success instead of silently dropping records.
func (s *MarketDataStore) CreatePrices(ctx context.Context, prices []*entity.StoredPrice) (int, error) {
	count := 0
	var errs []string
	for _, p := range prices {
		if _, err := s.CreatePrice(ctx, p); err != nil {
			errs = append(errs, fmt.Sprintf("%s/%s: %v", p.AssetID, p.BaseAssetID, err))
		} else {
			count++
		}
	}
	if len(errs) > 0 {
		return count, fmt.Errorf("%d price(s) failed: %s", len(errs), strings.Join(errs, "; "))
	}
	return count, nil
}

// GetLatestPrice returns the most recent price for asset/base/source.
func (s *MarketDataStore) GetLatestPrice(ctx context.Context, assetID, baseAssetID, sourceID string) (*entity.StoredPrice, error) {
	if assetID == "" {
		return nil, fmt.Errorf("%w: asset_id is required", store.ErrInvalidArgument)
	}

	// base_asset_id and source_id are optional filters: empty means "any". Omitting
	// base_asset_id yields the asset's latest price in whatever pair it trades against,
	// which portfolio valuation uses to convert via cross rates.
	args := []any{assetID}
	filters := ""
	if baseAssetID != "" {
		args = append(args, baseAssetID)
		filters += fmt.Sprintf(" AND base_asset_id = $%d", len(args))
	}
	if sourceID != "" {
		args = append(args, sourceID)
		filters += fmt.Sprintf(" AND source_id = $%d", len(args))
	}

	query := fmt.Sprintf(`
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp, provenance
		FROM prices
		WHERE asset_id = $1%s
		ORDER BY timestamp DESC
		LIMIT 1`, filters)

	var price entity.StoredPrice
	var provenance *string
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&price.ID,
		&price.SourceID,
		&price.AssetID,
		&price.BaseAssetID,
		&price.Interval,
		&price.Decimals,
		&price.Last,
		&price.Open,
		&price.High,
		&price.Low,
		&price.Close,
		&price.Volume,
		&price.MarketCap,
		&price.Timestamp,
		&provenance,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: price not found", store.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get latest price: %w", err)
	}
	price.Provenance = readProvenance(provenance)

	return &price, nil
}

// GetFirstPriceAfter returns the earliest stored price for assetID/baseAssetID at or after `after`.
func (s *MarketDataStore) GetFirstPriceAfter(ctx context.Context, assetID, baseAssetID string, after time.Time) (*entity.StoredPrice, error) {
	if assetID == "" || baseAssetID == "" {
		return nil, fmt.Errorf("%w: asset_id and base_asset_id are required", store.ErrInvalidArgument)
	}

	query := `
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp, provenance
		FROM prices
		WHERE asset_id = $1 AND base_asset_id = $2 AND timestamp >= $3
		ORDER BY timestamp ASC
		LIMIT 1`

	var price entity.StoredPrice
	var provenance *string
	err := s.pool.QueryRow(ctx, query, assetID, baseAssetID, after).Scan(
		&price.ID,
		&price.SourceID,
		&price.AssetID,
		&price.BaseAssetID,
		&price.Interval,
		&price.Decimals,
		&price.Last,
		&price.Open,
		&price.High,
		&price.Low,
		&price.Close,
		&price.Volume,
		&price.MarketCap,
		&price.Timestamp,
		&provenance,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: price not found after %s", store.ErrNotFound, after)
		}
		return nil, fmt.Errorf("failed to get first price after: %w", err)
	}
	price.Provenance = readProvenance(provenance)

	return &price, nil
}

// ListPriceHistory returns prices for an asset/base in a time range with pagination.
func (s *MarketDataStore) ListPriceHistory(ctx context.Context, opts marketdata.ListPriceHistoryOpts) ([]*entity.StoredPrice, string, error) {
	if opts.AssetID == "" || opts.BaseAssetID == "" {
		return nil, "", fmt.Errorf("%w: asset_id and base_asset_id are required", store.ErrInvalidArgument)
	}

	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{opts.AssetID, opts.BaseAssetID}
	argIdx := 3
	whereClauses := []string{"asset_id = $1", "base_asset_id = $2"}

	if opts.SourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("source_id = $%d", argIdx))
		args = append(args, opts.SourceID)
		argIdx++
	}

	if opts.From != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argIdx))
		args = append(args, *opts.From)
		argIdx++
	}

	if opts.To != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argIdx))
		args = append(args, *opts.To)
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil {
			var cursorTs time.Time
			if err := cursorTs.UnmarshalText(decoded); err == nil {
				whereClauses = append(whereClauses, fmt.Sprintf("timestamp > $%d", argIdx))
				args = append(args, cursorTs)
				argIdx++
			}
		}
	}

	query := fmt.Sprintf(`
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp, provenance
		FROM prices
		WHERE %s
		ORDER BY timestamp
		LIMIT $%d`,
		strings.Join(whereClauses, " AND "), argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list price history: %w", err)
	}
	defer rows.Close()

	prices := make([]*entity.StoredPrice, 0, limit)
	for rows.Next() {
		var price entity.StoredPrice
		var provenance *string
		if err := rows.Scan(
			&price.ID,
			&price.SourceID,
			&price.AssetID,
			&price.BaseAssetID,
			&price.Interval,
			&price.Decimals,
			&price.Last,
			&price.Open,
			&price.High,
			&price.Low,
			&price.Close,
			&price.Volume,
			&price.MarketCap,
			&price.Timestamp,
			&provenance,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan price: %w", err)
		}
		price.Provenance = readProvenance(provenance)
		prices = append(prices, &price)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate prices: %w", err)
	}

	var nextPageToken string
	if len(prices) > limit {
		lastPrice := prices[limit-1]
		prices = prices[:limit]
		txt, _ := lastPrice.Timestamp.MarshalText()
		nextPageToken = base64.StdEncoding.EncodeToString(txt)
	}

	return prices, nextPageToken, nil
}

// DeletePrice deletes a price record by ID.
func (s *MarketDataStore) DeletePrice(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: price ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid price ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM prices WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete price: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: price with ID %s", store.ErrNotFound, id)
	}

	return nil
}

// DeletePrices deletes price records by criteria.
func (s *MarketDataStore) DeletePrices(ctx context.Context, opts marketdata.DeletePricesOpts) error {
	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.AssetID != "" {
		if !isValidUUID(opts.AssetID) {
			return fmt.Errorf("%w: invalid asset_id format", store.ErrInvalidArgument)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("asset_id = $%d", argIdx))
		args = append(args, opts.AssetID)
		argIdx++
	}

	if opts.BaseAssetID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("base_asset_id = $%d", argIdx))
		args = append(args, opts.BaseAssetID)
		argIdx++
	}

	if opts.SourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("source_id = $%d", argIdx))
		args = append(args, opts.SourceID)
		argIdx++
	}

	if opts.From != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argIdx))
		args = append(args, *opts.From)
		argIdx++
	}

	if opts.To != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argIdx))
		args = append(args, *opts.To)
	}

	if len(whereClauses) == 0 {
		return fmt.Errorf("%w: at least one filter criteria is required", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM prices WHERE %s`, strings.Join(whereClauses, " AND ")), args...)
	if err != nil {
		return fmt.Errorf("failed to delete prices: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: no prices matching criteria found", store.ErrNotFound)
	}

	return nil
}

// Helper functions

func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func isConstraintError(err error) bool {
	return strings.Contains(err.Error(), "constraint") ||
		strings.Contains(err.Error(), "violates") ||
		strings.Contains(err.Error(), "duplicate key")
}

func assetTypeToString(t entity.AssetType) string {
	switch t {
	case entity.AssetTypeCryptocurrency:
		return "cryptocurrency"
	case entity.AssetTypeStock:
		return "stock"
	case entity.AssetTypeBond:
		return "bond"
	case entity.AssetTypeCommodity:
		return "commodity"
	case entity.AssetTypeForex:
		return "forex"
	case entity.AssetTypeFund:
		return "fund"
	default:
		return "unspecified"
	}
}

func stringToAssetType(s string) entity.AssetType {
	switch s {
	case "cryptocurrency":
		return entity.AssetTypeCryptocurrency
	case "stock":
		return entity.AssetTypeStock
	case "bond":
		return entity.AssetTypeBond
	case "commodity":
		return entity.AssetTypeCommodity
	case "forex":
		return entity.AssetTypeForex
	case "fund":
		return entity.AssetTypeFund
	default:
		return entity.AssetTypeUnspecified
	}
}

// PricingStatus aggregates the attempt log per asset.
//
// The aggregate is what makes the answer meaningful: attempts are recorded per
// (asset, source), so "nothing priced this" is a statement about all of them at
// once, not about the row that happened to be read. bool_or is the whole test —
// one source succeeding anywhere in the history is enough for the asset to have
// a price row, and therefore enough for absence not to be the explanation.
func (s *MarketDataStore) PricingStatus(ctx context.Context, assetIDs []string) ([]*entity.AssetPricingStatus, error) {
	if len(assetIDs) == 0 {
		return nil, nil
	}

	const query = `
		SELECT asset_id,
		       bool_or(succeeded_at IS NOT NULL) AS ever_priced,
		       min(attempted_at) AS first_asked_at,
		       max(attempted_at) AS last_asked_at,
		       count(DISTINCT source_id) AS sources_asked
		FROM price_fetch_attempts
		WHERE asset_id = ANY($1)
		GROUP BY asset_id`

	rows, err := s.pool.Query(ctx, query, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to read pricing status: %w", err)
	}
	defer rows.Close()

	out := make([]*entity.AssetPricingStatus, 0, len(assetIDs))
	for rows.Next() {
		var st entity.AssetPricingStatus
		var sources int64
		if err := rows.Scan(&st.AssetID, &st.EverPriced, &st.FirstAskedAt, &st.LastAskedAt, &sources); err != nil {
			return nil, fmt.Errorf("failed to scan pricing status: %w", err)
		}
		st.SourcesAsked = uint32(sources) // #nosec G115 -- count of sources, bounded by the provider registry
		out = append(out, &st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate pricing status: %w", err)
	}
	return out, nil
}
