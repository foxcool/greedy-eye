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
	// ListStalePricingTargets returns assets due for a price refresh from one
	// source, oldest deadline first. Everything it leaves out is either already
	// in the database or backed off after repeated misses — re-requesting it
	// would spend provider quota to learn nothing.
	ListStalePricingTargets(ctx context.Context, opts StalePricingOpts) ([]*entity.Asset, error)
	// RecordPriceAttempts records one sweep's outcome per asset. A miss pushes
	// the next attempt out exponentially, so assets the provider does not list
	// drain out of the rotation instead of blocking its head.
	RecordPriceAttempts(ctx context.Context, opts RecordAttemptsOpts) error
	// SetAssetVerdict writes an identity verdict (scam-filtering axis 1). A user
	// verdict (source "user:*") is terminal: an automated write never overwrites
	// one. The bool reports whether the row was written.
	SetAssetVerdict(ctx context.Context, assetID, verdict string, score *float64, signals map[string]float64, source string) (bool, error)
	// FindAssetIDByExternalRef resolves an asset by its identifier in an external
	// namespace (contract on a chain, provider coin id). ErrNotFound when unmapped.
	FindAssetIDByExternalRef(ctx context.Context, source, ref string) (string, error)
	// CreateAssetExternalRef maps an asset to an external identifier; a
	// conflicting (source, ref) yields ErrConstraint (identity is stable).
	CreateAssetExternalRef(ctx context.Context, ref *entity.AssetExternalRef) (*entity.AssetExternalRef, error)
	// ListAssetExternalRefs returns every external ref of the given assets in
	// one round trip. FindAssetIDByExternalRef answers the sync direction
	// (ref -> asset); pricing needs the reverse — which chain a contract lives
	// on — and one query per asset would be hundreds of round trips per sweep.
	ListAssetExternalRefs(ctx context.Context, assetIDs []string) ([]*entity.AssetExternalRef, error)
	// FindTickerIncumbent answers whether this asset's ticker is already held on
	// one of its own chains by an older, price-listed asset bound to a different
	// contract. One chain cannot carry two contracts of the same asset, so the
	// answer names an asset the given one is claiming to be. ErrNotFound when the
	// ticker is unclaimed.
	FindTickerIncumbent(ctx context.Context, assetID string) (string, error)

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
	// IdentityVerdict filters by scam-filtering verdict when non-empty.
	IdentityVerdict string
	// IDs restricts the result to these asset IDs. Lets a caller that already
	// knows what it wants read exactly those rows instead of paging the whole
	// catalogue and filtering in Go.
	IDs []string
}

// StalePricingOpts selects what one sweep refreshes from one source.
type StalePricingOpts struct {
	// SourceID scopes freshness to one provider: an asset Binance priced a
	// minute ago is still stale for CoinGecko. Required.
	SourceID string
	// Now is the deadline the selection compares against; an asset is due when
	// its next attempt is not in the future.
	Now time.Time
	// Limit caps the result — this is the per-sweep budget, which is what makes
	// the long tail rotate in oldest-first instead of arriving as one spike.
	// Zero means no cap.
	Limit int
	// Symbols restricts the selection to these symbols. Used for the subset a
	// provider prices in a single request, which is not worth budgeting.
	Symbols []string
	// ExcludeSymbols is the complement of Symbols, for selecting everything the
	// provider charges per unit for.
	ExcludeSymbols []string
	// ExcludeVerdicts drops assets quarantined from the portfolio sums. Pricing
	// money that is not counted buys nothing.
	ExcludeVerdicts []string
}

// RecordAttemptsOpts records what one sweep asked a source for and what came back.
type RecordAttemptsOpts struct {
	SourceID string
	At       time.Time
	// Priced are the assets the provider returned a price for.
	Priced []string
	// Missed are the assets asked for that came back without one.
	Missed []string
	// TTL is how long a successful fetch counts as current.
	TTL time.Duration
	// MaxBackoff caps the exponential push-out applied to consecutive misses.
	MaxBackoff time.Duration
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
