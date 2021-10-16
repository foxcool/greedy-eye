package entities

import "github.com/shopspring/decimal"

type TradingOpportunity struct {
	From       *Token
	To         *Token
	Exchange   Exchange
	FromAmount decimal.Decimal
	ToAmount   decimal.Decimal
}
