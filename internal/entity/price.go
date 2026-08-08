package entity

import (
	"time"

	"github.com/shopspring/decimal"
)

// Price represents real-time price data for trading logic.
// Uses decimal.Decimal for precision in calculations.
type Price struct {
	// Source is the source of the price: exchange, broker, etc.
	Source     string
	BaseAsset  AssetSymbol
	QuoteAsset AssetSymbol
	// LastPrice is the last price of the asset
	LastPrice decimal.Decimal
	// Ask is the lowest price for buying
	Ask decimal.Decimal
	// Bid is the highest price for selling
	Bid  decimal.Decimal
	Time time.Time
}

// StoredPrice represents price data from database.
// Amounts are raw integers scaled by Decimals (value = amount / 10^Decimals), carried as
// decimal.Decimal and stored in NUMERIC columns. Optional OHLCV fields use NullDecimal.
type StoredPrice struct {
	ID          string
	SourceID    string
	AssetID     string
	BaseAssetID string
	Interval    string
	Decimals    uint32
	Last        decimal.Decimal
	Open        decimal.NullDecimal
	High        decimal.NullDecimal
	Low         decimal.NullDecimal
	Close       decimal.NullDecimal
	Volume      decimal.NullDecimal
	// MarketCap is the quote's market context, not a property of the asset: the
	// same asset carries a different cap at every instant, so it belongs to the
	// price row that observed it. Null means the source did not report one —
	// which is a different statement from a cap of zero.
	MarketCap decimal.NullDecimal
	Timestamp time.Time
}

// AssetPricingStatus is what asking an asset's price sources has produced so
// far, read back from the attempt log.
//
// It exists to separate two things a valuation reports identically today: an
// asset nothing has looked at yet, and one every source has looked at and none
// could price. The first is a gap in our own pipeline; the second has exhausted
// the sources available and says something about the instrument.
//
// It is evidence, not a verdict. Silence from every source is consistent with a
// delisting, a halt, a ticker no provider carries and a chain gone dark, and
// nothing here can tell those apart.
type AssetPricingStatus struct {
	AssetID string
	// EverPriced is true when some source returned a price at some point. Such
	// an asset has a price row, so a valuation that still failed on it failed
	// for a different reason than absence.
	EverPriced bool
	// FirstAskedAt and LastAskedAt bound the period over which the silence has
	// been observed. Zero only when there is no attempt record at all, which is
	// reported by omitting the asset rather than by an empty status.
	FirstAskedAt time.Time
	LastAskedAt  time.Time
	// SourcesAsked is how many distinct sources have been asked.
	SourcesAsked uint32
}
