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
	Timestamp   time.Time
}
