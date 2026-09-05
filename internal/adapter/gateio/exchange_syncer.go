package gateio

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// balanceDecimals is the scale exchange balances are stored at. It matches the
// Binance syncer so two venues holding one coin land on the same scale.
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
// The position is available + locked: Gate.io holds open orders OUT of the
// spendable figure, not inside it. The opposite of every Substrate chain here,
// where reserved is a subset of balance and adding it doubles the largest
// holding — and a mistake nothing reveals until somebody has an open order.
// Measured, not read off the field names: see the anchor test.
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

// parseAmount reads one decimal string. An omitted field is zero; a field that
// is present and unreadable is an error, because an amount with no scale is a
// wrong number waiting to be written, where a missing one is only a gap.
func parseAmount(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}
