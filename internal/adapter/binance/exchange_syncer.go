package binance

import (
	"context"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// balanceDecimals is the fixed scale exchange balances are stored at. Binance
// reports spot balances with 8 fractional digits; 8 covers all spot assets
// without loss.
const balanceDecimals = 8

// ExchangeSyncer adapts *Client to entity.ExchangeSyncer, reading spot balances
// from the credentialed Binance account.
type ExchangeSyncer struct {
	client *Client
}

// NewExchangeSyncer wraps a *Client as an entity.ExchangeSyncer.
func NewExchangeSyncer(client *Client) *ExchangeSyncer {
	return &ExchangeSyncer{client: client}
}

// SyncExchange returns the non-zero spot balances (free + locked) of the
// credentialed account, amounts as raw integers scaled by balanceDecimals.
func (s *ExchangeSyncer) SyncExchange(ctx context.Context) ([]entity.ExchangeBalance, error) {
	raw, err := s.client.fetchAccount(ctx)
	if err != nil {
		return nil, err
	}

	scale := decimal.New(1, balanceDecimals)
	balances := make([]entity.ExchangeBalance, 0, len(raw))
	for _, b := range raw {
		free, err := decimal.NewFromString(b.Free)
		if err != nil {
			return nil, fmt.Errorf("parse free balance for %s: %w", b.Asset, err)
		}
		locked, err := decimal.NewFromString(b.Locked)
		if err != nil {
			return nil, fmt.Errorf("parse locked balance for %s: %w", b.Asset, err)
		}
		total := free.Add(locked)
		if total.IsZero() {
			continue
		}
		// raw integer = total * 10^balanceDecimals
		balances = append(balances, entity.ExchangeBalance{
			Symbol:   b.Asset,
			Amount:   total.Mul(scale).Round(0).String(),
			Decimals: balanceDecimals,
		})
	}
	return balances, nil
}
