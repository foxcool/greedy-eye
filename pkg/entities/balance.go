package entities

import "github.com/shopspring/decimal"

type Balance struct {
	Exchange *Exchange
	Token    *Token
	Amount   decimal.Decimal
}
