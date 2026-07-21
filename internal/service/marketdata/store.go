package marketdata

import (
	"context"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Store defines the data access contract for MarketDataService.
// Interface is defined here (consumer) per Go idiom "accept interfaces, return structs".
type Store interface {
	// Assets
	CreateAsset(ctx context.Context, asset *entity.Asset) (*entity.Asset, error)
	GetAsset(ctx context.Context, id string) (*entity.Asset, error)
	GetAssetBySymbol(ctx context.Context, symbol string) (*entity.Asset, error)
	// FindAssetByIdentity looks up an asset by its exact composite identity
	// (symbol, market, type); market and type must be concrete.
	FindAssetByIdentity(ctx context.Context, symbol, market string, typ entity.AssetType) (*entity.Asset, error)
	GetOrCreateAssetBySymbol(ctx context.Context, symbol, nameIfNew string, typeIfNew entity.AssetType) (*entity.Asset, error)
	UpdateAsset(ctx context.Context, asset *entity.Asset, fields []string) (*entity.Asset, error)
	DeleteAsset(ctx context.Context, id string) error
	ListAssets(ctx context.Context, opts ListAssetsOpts) ([]*entity.Asset, string, error)
	// SetAssetVerdict writes an identity verdict (scam-filtering axis 1). A user
	// verdict (source "user:*") is terminal: an automated write never overwrites
	// one. The bool reports whether the row was written.
	SetAssetVerdict(ctx context.Context, assetID, verdict string, score *float64, signals map[string]float64, source string) (bool, error)

	// Prices
	CreatePrice(ctx context.Context, price *entity.StoredPrice) (*entity.StoredPrice, error)
	CreatePrices(ctx context.Context, prices []*entity.StoredPrice) (int, error)
	// GetLatestPrice returns the asset's most recent price. An empty baseAssetID or
	// sourceID means "any" for that filter; omitting baseAssetID yields the price in
	// whatever pair the asset trades against (used for cross-rate valuation).
	GetLatestPrice(ctx context.Context, assetID, baseAssetID, sourceID string) (*entity.StoredPrice, error)
	ListPriceHistory(ctx context.Context, opts ListPriceHistoryOpts) ([]*entity.StoredPrice, string, error)
	DeletePrice(ctx context.Context, id string) error
	DeletePrices(ctx context.Context, opts DeletePricesOpts) error
}

// ListAssetsOpts contains options for listing assets.
type ListAssetsOpts struct {
	PageSize  int
	PageToken string
	Tags      []string
}

// ListPriceHistoryOpts contains options for listing price history.
type ListPriceHistoryOpts struct {
	AssetID     string
	BaseAssetID string
	SourceID    string
	Interval    string
	From        *time.Time
	To          *time.Time
	PageSize    int
	PageToken   string
}

// DeletePricesOpts contains options for batch deleting prices.
type DeletePricesOpts struct {
	AssetID     string
	BaseAssetID string
	SourceID    string
	From        *time.Time
	To          *time.Time
}
