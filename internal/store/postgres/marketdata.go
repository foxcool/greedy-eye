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

	asset.ID = uuid.New().String()
	tagsJSON, err := json.Marshal(asset.Tags)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tags: %w", err)
	}

	query := `
		INSERT INTO assets (uuid, symbol, name, type, tags, created_at, updated_at)
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
		SELECT uuid, symbol, name, type, tags, created_at, updated_at
		FROM assets
		WHERE uuid = $1`

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

// UpdateAsset updates an asset with the specified fields.
func (s *MarketDataStore) UpdateAsset(ctx context.Context, asset *entity.Asset, fields []string) (*entity.Asset, error) {
	if asset == nil || asset.ID == "" {
		return nil, fmt.Errorf("%w: asset with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(asset.ID) {
		return nil, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	// Build dynamic UPDATE query based on fields
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
		WHERE uuid = $1
		RETURNING uuid, symbol, name, type, tags, created_at, updated_at`,
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

	query := `DELETE FROM assets WHERE uuid = $1`
	result, err := s.pool.Exec(ctx, query, id)
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

	// Handle cursor pagination
	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	// Handle tags filtering using JSONB @> operator
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
		SELECT uuid, symbol, name, type, tags, created_at, updated_at
		FROM assets
		%s
		ORDER BY uuid
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1) // Fetch one extra to detect next page

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
		// Remove the extra item and create next page token
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

	// Verify assets exist and get their internal IDs
	assetInternalID, err := s.getAssetInternalID(ctx, price.AssetID)
	if err != nil {
		return nil, err
	}
	baseAssetInternalID, err := s.getAssetInternalID(ctx, price.BaseAssetID)
	if err != nil {
		return nil, err
	}

	price.ID = uuid.New().String()
	if price.Timestamp.IsZero() {
		price.Timestamp = time.Now()
	}

	query := `
		INSERT INTO prices (uuid, source_id, asset_id, base_asset_id, interval, decimals, last, open, high, low, close, volume, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING timestamp`

	err = s.pool.QueryRow(ctx, query,
		price.ID,
		price.SourceID,
		assetInternalID,
		baseAssetInternalID,
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
func (s *MarketDataStore) CreatePrices(ctx context.Context, prices []*entity.StoredPrice) (int, error) {
	count := 0
	for _, p := range prices {
		_, err := s.CreatePrice(ctx, p)
		if err == nil {
			count++
		}
	}
	return count, nil
}

// GetLatestPrice returns the most recent price for asset/base/source.
func (s *MarketDataStore) GetLatestPrice(ctx context.Context, assetID, baseAssetID, sourceID string) (*entity.StoredPrice, error) {
	if assetID == "" || baseAssetID == "" {
		return nil, fmt.Errorf("%w: asset_id and base_asset_id are required", store.ErrInvalidArgument)
	}

	assetInternalID, err := s.getAssetInternalID(ctx, assetID)
	if err != nil {
		return nil, err
	}
	baseAssetInternalID, err := s.getAssetInternalID(ctx, baseAssetID)
	if err != nil {
		return nil, err
	}

	args := []any{assetInternalID, baseAssetInternalID}
	sourceFilter := ""
	if sourceID != "" {
		sourceFilter = "AND source_id = $3"
		args = append(args, sourceID)
	}

	query := fmt.Sprintf(`
		SELECT p.uuid, p.source_id, a.uuid, ba.uuid, p.interval, p.decimals, p.last, p.open, p.high, p.low, p.close, p.volume, p.timestamp
		FROM prices p
		JOIN assets a ON p.asset_id = a.id
		JOIN assets ba ON p.base_asset_id = ba.id
		WHERE p.asset_id = $1 AND p.base_asset_id = $2 %s
		ORDER BY p.timestamp DESC
		LIMIT 1`, sourceFilter)

	var price entity.StoredPrice
	err = s.pool.QueryRow(ctx, query, args...).Scan(
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

// ListPriceHistory returns prices for an asset/base in a time range with pagination.
func (s *MarketDataStore) ListPriceHistory(ctx context.Context, opts marketdata.ListPriceHistoryOpts) ([]*entity.StoredPrice, string, error) {
	if opts.AssetID == "" || opts.BaseAssetID == "" {
		return nil, "", fmt.Errorf("%w: asset_id and base_asset_id are required", store.ErrInvalidArgument)
	}

	assetInternalID, err := s.getAssetInternalID(ctx, opts.AssetID)
	if err != nil {
		return nil, "", err
	}
	baseAssetInternalID, err := s.getAssetInternalID(ctx, opts.BaseAssetID)
	if err != nil {
		return nil, "", err
	}

	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{assetInternalID, baseAssetInternalID}
	argIdx := 3
	whereClauses := []string{"p.asset_id = $1", "p.base_asset_id = $2"}

	if opts.SourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("p.source_id = $%d", argIdx))
		args = append(args, opts.SourceID)
		argIdx++
	}

	if opts.From != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("p.timestamp >= $%d", argIdx))
		args = append(args, *opts.From)
		argIdx++
	}

	if opts.To != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("p.timestamp <= $%d", argIdx))
		args = append(args, *opts.To)
		argIdx++
	}

	// Handle cursor pagination by timestamp
	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil {
			var cursorTs time.Time
			if err := cursorTs.UnmarshalText(decoded); err == nil {
				whereClauses = append(whereClauses, fmt.Sprintf("p.timestamp > $%d", argIdx))
				args = append(args, cursorTs)
				argIdx++
			}
		}
	}

	query := fmt.Sprintf(`
		SELECT p.uuid, p.source_id, a.uuid, ba.uuid, p.interval, p.decimals, p.last, p.open, p.high, p.low, p.close, p.volume, p.timestamp
		FROM prices p
		JOIN assets a ON p.asset_id = a.id
		JOIN assets ba ON p.base_asset_id = ba.id
		WHERE %s
		ORDER BY p.timestamp
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

	query := `DELETE FROM prices WHERE uuid = $1`
	result, err := s.pool.Exec(ctx, query, id)
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
		assetInternalID, err := s.getAssetInternalID(ctx, opts.AssetID)
		if err != nil {
			return err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("asset_id = $%d", argIdx))
		args = append(args, assetInternalID)
		argIdx++
	}

	if opts.BaseAssetID != "" {
		baseAssetInternalID, err := s.getAssetInternalID(ctx, opts.BaseAssetID)
		if err != nil {
			return err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("base_asset_id = $%d", argIdx))
		args = append(args, baseAssetInternalID)
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

	query := fmt.Sprintf(`DELETE FROM prices WHERE %s`, strings.Join(whereClauses, " AND "))
	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete prices: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: no prices matching criteria found", store.ErrNotFound)
	}

	return nil
}

// Helper functions

func (s *MarketDataStore) getAssetInternalID(ctx context.Context, uuid string) (int64, error) {
	if !isValidUUID(uuid) {
		return 0, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM assets WHERE uuid = $1", uuid).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: asset not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get asset: %w", err)
	}
	return id, nil
}

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
