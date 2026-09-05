package gateio

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// balanceDecimals is the fixed scale exchange balances are stored at, matching
// the Binance syncer next door so two venues holding one coin land on the same
// scale. Gate.io quotes spot amounts to 8 fractional digits.
const balanceDecimals = 8

// ExchangeSyncer adapts *Client to entity.ExchangeSyncer.
type ExchangeSyncer struct {
	client *Client
}

// NewExchangeSyncer wraps a *Client as an entity.ExchangeSyncer.
func NewExchangeSyncer(client *Client) *ExchangeSyncer {
	return &ExchangeSyncer{client: client}
}

// SyncExchange returns the non-zero spot balances of the credentialed account,
// amounts as raw integers scaled by balanceDecimals.
//
// available + locked, and the sum is the position: Gate.io reports the two as
// DISJOINT pools — locked is what sits in open orders, held out of available
// rather than inside it. That is the opposite of every Substrate chain in this
// tree, where reserved is a subset of balance and adding it doubles the largest
// holding. Which way a venue reports it cannot be guessed from the field names,
// and getting it wrong is invisible until someone has an open order.
//
// An unparseable amount is an error rather than a skip. It has no scale, and a
// position written at the wrong scale is a wrong number where a missing one
// would only have been a gap.
func (s *ExchangeSyncer) SyncExchange(ctx context.Context) ([]entity.ExchangeBalance, error) {
	raw, err := s.client.fetchSpotAccounts(ctx)
	if err != nil {
		return nil, err
	}

	scale := decimal.New(1, balanceDecimals)
	balances := make([]entity.ExchangeBalance, 0, len(raw))
	for _, b := range raw {
		available, err := parseAmount(b.Available)
		if err != nil {
			return nil, fmt.Errorf("parse available balance for %s: %w", b.Currency, err)
		}
		locked, err := parseAmount(b.Locked)
		if err != nil {
			return nil, fmt.Errorf("parse locked balance for %s: %w", b.Currency, err)
		}

		total := available.Add(locked)
		if total.IsZero() {
			continue
		}
		balances = append(balances, entity.ExchangeBalance{
			Symbol:   b.Currency,
			Amount:   total.Mul(scale).Round(0).String(),
			Decimals: balanceDecimals,
		})
	}
	return balances, nil
}

// parseAmount reads one decimal string. An omitted field is zero — Gate.io
// leaves `locked` out for a currency that has never had an order — while a
// field that is present and unreadable is an error, because those are different
// statements and only the first one is safe to assume.
func parseAmount(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}
