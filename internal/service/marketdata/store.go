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
	UpdateAsset(ctx context.Context, asset *entity.Asset, fields []string) (*entity.Asset, error)
	DeleteAsset(ctx context.Context, id string) error
	ListAssets(ctx context.Context, opts ListAssetsOpts) ([]*entity.Asset, string, error)

	// Prices
	CreatePrice(ctx context.Context, price *entity.StoredPrice) (*entity.StoredPrice, error)
	CreatePrices(ctx context.Context, prices []*entity.StoredPrice) (int, error)
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
