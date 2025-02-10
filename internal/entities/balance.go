package entities

import "github.com/shopspring/decimal"

type Balance struct {
	Exchange *Exchange
	Token    *Asset
	Amount   decimal.Decimal
}
