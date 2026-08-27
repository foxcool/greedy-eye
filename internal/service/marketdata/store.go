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
	// ResetPriceAttempts forgives the back-off accrued against one source: the
	// miss counter goes to zero and every asset it holds becomes due now. It
	// does not delete the rows — attempted_at and succeeded_at stay, because
	// they are the record of what was asked and when, and only the schedule
	// derived from them is wrong. Returns how many assets were freed.
	ResetPriceAttempts(ctx context.Context, sourceID string, at time.Time) (int64, error)
	// PricingStatus reads the attempt log back for the given assets: whether any
	// source ever priced each one, over what period it has been asked about, and
	// by how many sources. Assets with no attempt record are omitted — there is
	// nothing recorded to report, and an empty status would read as "asked, and
	// nothing came back", which is the opposite claim.
	PricingStatus(ctx context.Context, assetIDs []string) ([]*entity.AssetPricingStatus, error)
	// SweepSchedule aggregates the attempt log into what the next sweep would
	// find: how many assets are due, how many are held back by their own
	// back-off, how far out the queue reaches. Counted from the same table the
	// selection reads, so it cannot disagree with the sweep it describes.
	SweepSchedule(ctx context.Context, opts SweepScheduleOpts) ([]*entity.SourceSchedule, error)
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
	// DeleteAssetExternalRef removes one binding, refusing a ref that belongs to
	// another asset. ErrNotFound when there is no such ref on this asset.
	DeleteAssetExternalRef(ctx context.Context, assetID, id string) error
	// CreateAssetRiskFlag records a situational risk on an asset (risk-model axis
	// 2). Flags accumulate: two exploits are two events, not one overwritten.
	// A flag never affects a valuation — it marks real money that may need
	// acting on, which is the opposite claim from an identity verdict.
	CreateAssetRiskFlag(ctx context.Context, flag *entity.AssetRiskFlag) (*entity.AssetRiskFlag, error)
	// ListAssetRiskFlags returns one asset's flags, newest first. Single-asset
	// on purpose: its only consumer is the asset card, and a batch form would
	// invite the catalogue list to carry flags it has no use for.
	ListAssetRiskFlags(ctx context.Context, assetID string) ([]*entity.AssetRiskFlag, error)
	// DeleteAssetRiskFlag removes one flag, refusing one that belongs to another
	// asset. ErrNotFound when there is no such flag on this asset.
	DeleteAssetRiskFlag(ctx context.Context, assetID, id string) error
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

// SweepScheduleOpts selects which sources to report a queue for.
type SweepScheduleOpts struct {
	// SourceIDs limits the report to these sources. Empty reports every source
	// present in the attempt log.
	SourceIDs []string
	// Now is the boundary between due and deferred, the same comparison
	// ListStalePricingTargets makes.
	Now time.Time
	// ExcludeVerdicts drops quarantined assets, matching what the sweep itself
	// selects. Counting them would report work nobody intends to do.
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
