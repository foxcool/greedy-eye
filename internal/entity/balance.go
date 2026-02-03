package entity

import "github.com/shopspring/decimal"

type Balance struct {
	Exchange *Exchange
	Token    *AssetSymbol
	Amount   decimal.Decimal
}
