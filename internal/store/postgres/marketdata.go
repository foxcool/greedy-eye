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

// GetOrCreateAssetBySymbol returns an existing asset by symbol or creates a new one.
// typeIfNew and nameIfNew are used only when creating. Safe under concurrent inserts:
// if a concurrent write wins the race, the existing row is returned.
func (s *MarketDataStore) GetOrCreateAssetBySymbol(ctx context.Context, symbol, nameIfNew string, typeIfNew entity.AssetType) (*entity.Asset, error) {
	a, err := s.GetAssetBySymbol(ctx, symbol)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	created, err := s.CreateAsset(ctx, &entity.Asset{
		Symbol: symbol,
		Name:   nameIfNew,
		Type:   typeIfNew,
		Tags:   []string{},
	})
	if err != nil {
		// Concurrent insert: another process created it first — read back.
		if errors.Is(err, store.ErrConstraint) {
			return s.GetAssetBySymbol(ctx, symbol)
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
func (s *MarketDataStore) GetAssetBySymbol(ctx context.Context, symbol string) (*entity.Asset, error) {
	symbol = entity.NormalizeSymbol(symbol)
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is required", store.ErrInvalidArgument)
	}

	// LIMIT 2: one row past the unique match is enough to detect ambiguity.
	query := `
		SELECT ` + assetColumns + `
		FROM assets
		WHERE symbol = $1
		LIMIT 2`

	rows, err := s.pool.Query(ctx, query, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get asset by symbol: %w", err)
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
		return nil, fmt.Errorf("failed to get asset by symbol: %w", err)
	}

	switch len(assets) {
	case 0:
		return nil, fmt.Errorf("%w: asset with symbol %s", store.ErrNotFound, symbol)
	case 1:
		return assets[0], nil
	default:
		return nil, fmt.Errorf("%w: symbol %s is ambiguous across markets, look up by ID or specify market", store.ErrInvalidArgument, symbol)
	}
}

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
		INSERT INTO prices (id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
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
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp
		FROM prices
		WHERE asset_id = $1%s
		ORDER BY timestamp DESC
		LIMIT 1`, filters)

	var price entity.StoredPrice
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
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: price not found", store.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get latest price: %w", err)
	}

	return &price, nil
}

// GetFirstPriceAfter returns the earliest stored price for assetID/baseAssetID at or after `after`.
func (s *MarketDataStore) GetFirstPriceAfter(ctx context.Context, assetID, baseAssetID string, after time.Time) (*entity.StoredPrice, error) {
	if assetID == "" || baseAssetID == "" {
		return nil, fmt.Errorf("%w: asset_id and base_asset_id are required", store.ErrInvalidArgument)
	}

	query := `
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp
		FROM prices
		WHERE asset_id = $1 AND base_asset_id = $2 AND timestamp >= $3
		ORDER BY timestamp ASC
		LIMIT 1`

	var price entity.StoredPrice
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
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: price not found after %s", store.ErrNotFound, after)
		}
		return nil, fmt.Errorf("failed to get first price after: %w", err)
	}

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
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, market_cap, timestamp
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
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan price: %w", err)
		}
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
