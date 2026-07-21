package cosmos

import (
	"context"
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// HandlesAddress reports whether an address belongs to a Cosmos zone this
// adapter serves, routing accounts that name no chain to it.
//
// Both the checksum and the prefix have to hold: bech32 is also Bitcoin's
// SegWit encoding, and the prefix is the only thing separating a Cosmos
// address from a "bc1…" one.
func HandlesAddress(address string) bool {
	hrp, _, err := decodeBech32(address)
	if err != nil {
		return false
	}
	_, ok := networkByHRP(hrp)
	return ok
}

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet returns the native balance of the address on each requested chain,
// staked value included.
//
// With no chains named it sweeps every zone this adapter knows. That works
// because one key yields one address per chain, differing only in its prefix —
// so the address is re-encoded per zone before asking, which each chain's LCD
// requires: unlike Subscan, it accepts nothing but its own prefix.
//
// Per-chain failures are joined into the returned error while the balances
// gathered so far are still returned, so one unreachable zone does not lose
// the others.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	if len(chains) == 0 {
		chains = SupportedChains()
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

		chainAddress, err := reencode(address, net.hrp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", chain, err))
			continue
		}

		balance, err := a.client.GetBalance(ctx, chain, chainAddress)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", chain, err))
			continue
		}

		total := balance.Total()
		if total.IsZero() {
			continue // nothing held here; not an error
		}

		// The LCD reports micro-units, which is already the raw integer form.
		balances = append(balances, entity.WalletBalance{
			Symbol:   net.symbol,
			Name:     net.name,
			Amount:   total.BigInt().String(),
			Decimals: net.decimals,
			Chain:    chain,
		})
	}

	return balances, errors.Join(errs...)
}
