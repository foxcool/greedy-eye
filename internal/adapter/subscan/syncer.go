package subscan

import (
	"context"
	"errors"
	"fmt"

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

// SyncWallet returns the native balance of the address on each requested chain.
//
// Unlike the EVM syncer this one cannot auto-discover: Substrate addresses are
// SS58-encoded per network, so the same key yields a different address on every
// chain and there is nothing to enumerate. Callers must name the chains — the
// syncer registry guarantees this by never routing an empty chain list here.
//
// Per-chain failures are joined into the returned error while the balances
// gathered so far are still returned, so one unreachable network does not lose
// the others.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	if len(chains) == 0 {
		return nil, errors.New("subscan: chains must be named explicitly (no auto-discovery on Substrate)")
	}

	var (
		balances []entity.WalletBalance
		errs     []error
	)
	for _, chain := range chains {
		net, ok := networks[chain]
		if !ok {
			errs = append(errs, fmt.Errorf("unsupported chain %q", chain))
			continue
		}

		account, err := a.client.GetAccount(ctx, chain, address)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", chain, err))
			continue
		}

		total := account.Total()
		if total.IsZero() {
			continue // nothing held here; not an error
		}

		// Subscan reports whole token units; holdings are stored as raw
		// integers scaled by the chain's decimals.
		raw := total.Shift(int32(net.decimals)).BigInt().String()

		balances = append(balances, entity.WalletBalance{
			Symbol:   net.symbol,
			Name:     net.name,
			Amount:   raw,
			Decimals: net.decimals,
		})
	}

	return balances, errors.Join(errs...)
}
