package blockchair

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/base58"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// legacyLen is the decoded size of a base58 address: version byte, 20-byte
// hash, 4-byte checksum. Every chain here uses this layout.
const legacyLen = 25

// HandlesAddress reports whether an address belongs to a chain this adapter
// serves, routing accounts that name no chain to it.
//
// The version byte is the whole of the distinction. Dash, Dogecoin and Bitcoin
// addresses are byte-identical in structure, so matching on the leading
// character would be matching on a rendering artifact — and claiming a Bitcoin
// address here would resolve it against the wrong chain, where it has never
// been seen, reporting an empty wallet rather than an error.
func HandlesAddress(address string) bool {
	decoded, err := base58.Decode(address)
	if err != nil || len(decoded) != legacyLen {
		return false
	}
	for _, net := range networks {
		if decoded[0] == net.version {
			return true
		}
	}
	return false
}

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
// With no chains named it asks only the chain whose version byte the address
// carries. Unlike the Substrate and Cosmos sweeps, there is nothing to gain
// from probing the others: an address is valid on exactly one of these chains,
// so the rest would answer "never seen" by construction.
//
// Per-chain failures are joined into the returned error while the balances
// gathered so far are still returned.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	if len(chains) == 0 {
		chain, ok := chainForAddress(address)
		if !ok {
			return nil, fmt.Errorf("blockchair: address %q belongs to no supported chain", address)
		}
		chains = []string{chain}
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

		amount, err := a.client.GetBalance(ctx, chain, address)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", chain, err))
			continue
		}
		if amount <= 0 {
			continue // nothing held here; not an error
		}

		// Blockchair reports the smallest unit, which is already the raw
		// integer form.
		balances = append(balances, entity.WalletBalance{
			Symbol:   net.symbol,
			Name:     net.name,
			Amount:   strconv.FormatInt(amount, 10),
			Decimals: net.decimals,
		})
	}

	return balances, errors.Join(errs...)
}

// chainForAddress identifies the chain from the address's version byte.
func chainForAddress(address string) (string, bool) {
	decoded, err := base58.Decode(address)
	if err != nil || len(decoded) != legacyLen {
		return "", false
	}
	for chain, net := range networks {
		if decoded[0] == net.version {
			return chain, true
		}
	}
	return "", false
}
