package moralis

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

func (a *WalletSyncerAdapter) GetWalletTokenBalances(ctx context.Context, chain, address string) ([]entity.WalletBalance, error) {
	balances, err := a.client.GetWalletTokenBalances(ctx, chain, address)
	if err != nil {
		return nil, err
	}
	result := make([]entity.WalletBalance, 0, len(balances))
	for _, b := range balances {
		result = append(result, entity.WalletBalance{
			Symbol:   b.Symbol,
			Name:     b.Name,
			Amount:   b.Balance,
			Decimals: b.Decimals,
		})
	}
	return result, nil
}
