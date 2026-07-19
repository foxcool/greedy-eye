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
// Moonbeam is unreachable this way — its addresses are EVM H160, not SS58 —
// so it has to be named explicitly.
//
// Per-chain failures are joined into the returned error while the balances
// gathered so far are still returned, so one unreachable network does not lose
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
		raw := total.Shift(net.decimals).BigInt().String()

		balances = append(balances, entity.WalletBalance{
			Symbol:   net.symbol,
			Name:     net.name,
			Amount:   raw,
			Decimals: int(net.decimals),
		})
	}

	return balances, errors.Join(errs...)
}
