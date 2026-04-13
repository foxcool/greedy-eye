package moralis

import (
	"context"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// nativeToken returns the native coin metadata for a given Moralis chain identifier.
// Returns ("", "") when the chain is unknown.
var nativeToken = map[string]struct{ symbol, name string }{
	"eth":       {"ETH", "Ethereum"},
	"base":      {"ETH", "Ethereum"},
	"arbitrum":  {"ETH", "Ethereum"},
	"optimism":  {"ETH", "Ethereum"},
	"linea":     {"ETH", "Ethereum"},
	"zksync":    {"ETH", "Ethereum"},
	"scroll":    {"ETH", "Ethereum"},
	"polygon":   {"POL", "Polygon"},
	"bsc":       {"BNB", "BNB Chain"},
	"avalanche": {"AVAX", "Avalanche"},
	"fantom":    {"FTM", "Fantom"},
}

const nativeDecimals = 18

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
			Symbol:          b.Symbol,
			Name:            b.Name,
			Amount:          b.Balance,
			Decimals:        b.Decimals,
			ContractAddress: b.TokenAddress,
		})
	}
	return result, nil
}

func (a *WalletSyncerAdapter) GetActiveChains(ctx context.Context, address string) ([]string, error) {
	return a.client.GetActiveChains(ctx, address)
}

func (a *WalletSyncerAdapter) GetNativeBalance(ctx context.Context, chain, address string) (*entity.WalletBalance, error) {
	native, ok := nativeToken[chain]
	if !ok {
		return nil, fmt.Errorf("unsupported chain for native balance: %s", chain)
	}

	raw, err := a.client.GetWalletBalance(ctx, chain, address)
	if err != nil {
		return nil, err
	}
	if raw == "" || raw == "0" {
		return nil, nil
	}
	return &entity.WalletBalance{
		Symbol:   native.symbol,
		Name:     native.name,
		Amount:   raw,
		Decimals: nativeDecimals,
	}, nil
}
