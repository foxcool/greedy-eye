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
		INSERT INTO assets (id, symbol, name, type, tags, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		asset.ID,
		asset.Symbol,
		asset.Name,
		assetTypeToString(asset.Type),
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
		SELECT id, symbol, name, type, tags, created_at, updated_at
		FROM assets
		WHERE id = $1`

	var asset entity.Asset
	var typeStr string
	var tagsJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&asset.ID,
		&asset.Symbol,
		&asset.Name,
		&typeStr,
		&tagsJSON,
		&asset.CreatedAt,
		&asset.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: asset with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get asset: %w", err)
	}

	asset.Type = stringToAssetType(typeStr)
	if err := json.Unmarshal(tagsJSON, &asset.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
	}

	return &asset, nil
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

// GetAssetBySymbol returns an asset by its symbol (case-sensitive).
func (s *MarketDataStore) GetAssetBySymbol(ctx context.Context, symbol string) (*entity.Asset, error) {
	if symbol == "" {
		return nil, fmt.Errorf("%w: symbol is required", store.ErrInvalidArgument)
	}

	query := `
		SELECT id, symbol, name, type, tags, created_at, updated_at
		FROM assets
		WHERE symbol = $1`

	var asset entity.Asset
	var typeStr string
	var tagsJSON []byte

	err := s.pool.QueryRow(ctx, query, symbol).Scan(
		&asset.ID,
		&asset.Symbol,
		&asset.Name,
		&typeStr,
		&tagsJSON,
		&asset.CreatedAt,
		&asset.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: asset with symbol %s", store.ErrNotFound, symbol)
		}
		return nil, fmt.Errorf("failed to get asset by symbol: %w", err)
	}

	asset.Type = stringToAssetType(typeStr)
	if err := json.Unmarshal(tagsJSON, &asset.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
	}

	return &asset, nil
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
			args = append(args, asset.Symbol)
			argIdx++
		case "name":
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, asset.Name)
			argIdx++
		case "type":
			setClauses = append(setClauses, fmt.Sprintf("type = $%d", argIdx))
			args = append(args, assetTypeToString(asset.Type))
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
		RETURNING id, symbol, name, type, tags, created_at, updated_at`,
		strings.Join(setClauses, ", "))

	var result entity.Asset
	var typeStr string
	var tagsJSON []byte

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&result.ID,
		&result.Symbol,
		&result.Name,
		&typeStr,
		&tagsJSON,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: asset with ID %s", store.ErrNotFound, asset.ID)
		}
		return nil, fmt.Errorf("failed to update asset: %w", err)
	}

	result.Type = stringToAssetType(typeStr)
	if err := json.Unmarshal(tagsJSON, &result.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
	}

	return &result, nil
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

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT id, symbol, name, type, tags, created_at, updated_at
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
		var asset entity.Asset
		var typeStr string
		var tagsJSON []byte

		if err := rows.Scan(
			&asset.ID,
			&asset.Symbol,
			&asset.Name,
			&typeStr,
			&tagsJSON,
			&asset.CreatedAt,
			&asset.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan asset: %w", err)
		}

		asset.Type = stringToAssetType(typeStr)
		if err := json.Unmarshal(tagsJSON, &asset.Tags); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal tags: %w", err)
		}

		assets = append(assets, &asset)
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
		INSERT INTO prices (id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, timestamp
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
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, timestamp
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
		SELECT id, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, timestamp
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
