package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

type Price struct {
	// Source is the source of the price: exchange, broker, etc.
	Source     string
	BaseAsset  Asset
	QuoteAsset Asset
	// Price is the last price of the asset
	LastPrice decimal.Decimal
	// Ask is the lowest price for buying
	Ask decimal.Decimal
	// Bid is the highest price for selling
	Bid  decimal.Decimal
	Time time.Time
}
