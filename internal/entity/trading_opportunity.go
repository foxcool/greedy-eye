package entity

import (
	"github.com/shopspring/decimal"
)

type TradingOpportunity struct {
	From     *AssetSymbol
	To       *AssetSymbol
	Exchange Exchange

	FromAmount decimal.Decimal
	ToAmount   decimal.Decimal

	Fee    decimal.Decimal
	Profit decimal.Decimal
}
