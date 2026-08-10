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
	// BaseSymbol overrides the provider's declared quote currency for this row
	// only. Empty means the provider's BaseAssetSymbol() stands, which is the
	// case for every source that quotes everything in one currency.
	//
	// A broker prices a foreign share in dollars and a domestic one in roubles
	// from the same response, so one base per provider cannot describe it. The
	// handler resolves this to a BaseAssetID before persisting; it is never
	// stored as text.
	BaseSymbol string
	Interval   string
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
	// Provenance says what produced this number: a trade, or an administrative
	// act. Empty means the source made no claim either way, which is where every
	// row written before this field existed sits.
	Provenance PriceProvenance
}

// PriceProvenance is what stands behind a quote.
//
// ADR-009 gates a price on the market behind it, and until now "market" meant
// reported turnover. That silently assumed every print came from a trade. It
// does not: MOEX publishes LEGALCLOSEPRICE — a recognised close set
// administratively — for instruments that did not trade at all, and FXGD's last
// reachable figure was 93.55 ₽ that way against a real last traded price of 37 ₽.
// Same shape, same fields, 2.5x apart, and nothing in the row said which was which.
//
// It is deliberately not a boolean. "Not traded" covers at least two different
// claims — an exchange's recognised close and a fund's NAV — and the day the
// first RWA arrives (BUIDL: $2.7bn under management, no turnover) they must not
// have collapsed into one value already.
type PriceProvenance string

const (
	// PriceProvenanceUnknown is the honest value for a source that says nothing
	// about how its number came to be. Most crypto providers are here: their
	// prints come from trades, but they never promised that in the payload.
	PriceProvenanceUnknown PriceProvenance = ""
	// PriceProvenanceTraded means a trade produced this price.
	PriceProvenanceTraded PriceProvenance = "traded"
	// PriceProvenanceAppraised means the venue published the number without a
	// trade behind it: a recognised close, a settlement price, an admitted quote.
	PriceProvenanceAppraised PriceProvenance = "appraised"
)

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
