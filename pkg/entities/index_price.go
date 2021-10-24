package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

type IndexPrice struct {
	IndexName string
	Asset     Asset
	Price     decimal.Decimal
	Time      time.Time
}
