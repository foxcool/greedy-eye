package entities

import (
	"github.com/shopspring/decimal"
)

type TradingOpportunity struct {
	From     *Asset
	To       *Asset
	Exchange Exchange

	FromAmount decimal.Decimal
	ToAmount   decimal.Decimal

	Fee    decimal.Decimal
	Profit decimal.Decimal
}
