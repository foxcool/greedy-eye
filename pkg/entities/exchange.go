package entities

import "github.com/shopspring/decimal"

type Exchange struct {
	ID string

	// How many tokens we have on exchange.
	//
	Balance map[Asset]decimal.Decimal
}

const (
	ExchangeSora  = "sora"
	Exchange1inch = "1inch"
)
