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
// Uses int64 with decimals field for efficient storage.
type StoredPrice struct {
	ID          string
	SourceID    string
	AssetID     string
	BaseAssetID string
	Interval    string
	Decimals    uint32
	Last        int64
	Open        *int64
	High        *int64
	Low         *int64
	Close       *int64
	Volume      *int64
	Timestamp   time.Time
}
