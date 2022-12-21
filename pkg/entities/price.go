package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

type Price struct {
	Source string
	Asset  Asset
	Price  decimal.Decimal
	Time   time.Time
}
