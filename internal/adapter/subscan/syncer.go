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
// With no chains named it sweeps every network this adapter knows and keeps
// the ones holding a balance — the same approach the EVM syncer takes with its
// candidate list. That works because SS58 is just a re-encoding of one public
// key: Subscan resolves any network's form of an address, so a single account
// covers the whole ecosystem at the cost of one request per network.
//
// Moonbeam is skipped by the sweep — its addresses are EVM H160, not SS58, so
// probing it could only ever return "not found" — and has to be named
// explicitly instead.
//
// Per-chain failures are joined into the returned error while the balances
// gathered so far are still returned, so one unreachable network does not lose
// the others.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	if len(chains) == 0 {
		chains = sweepChains()
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

		// Already raw planck at the precision the API stated — no shift, and
		// no table lookup. The chain's decimals are a display detail here;
		// the response is the authority on how to read its own number.
		balances = append(balances, entity.WalletBalance{
			Symbol:   account.Symbol,
			Name:     net.name,
			Amount:   total.BigInt().String(),
			Decimals: int(account.Decimals),
		})
	}

	return balances, errors.Join(errs...)
}
