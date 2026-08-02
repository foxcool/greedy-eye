package cosmos

import (
	"context"
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
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

		if balance.Total().IsZero() {
			continue // nothing held here; not an error
		}

		// One row per pool instead of one summed row. The four pools are
		// disjoint by construction — the bank module, the staking module and
		// the distribution module each answer for their own — so emitting them
		// separately neither double-counts nor loses the total.
		//
		// Rewards ride with the liquid pool: they are one claim transaction
		// away from spendable, which is what the liquidity axis is about, and
		// they are NOT in the bank balance, which is why they arrive apart.
		// The sync merges same-key rows, so bank + rewards land as one liquid
		// position.
		//
		// The LCD reports micro-units, which is already the raw integer form.
		emit := func(amount decimal.Decimal, liquidity entity.Liquidity) {
			if amount.IsZero() {
				return
			}
			balances = append(balances, entity.WalletBalance{
				Symbol:    net.symbol,
				Name:      net.name,
				Amount:    amount.BigInt().String(),
				Decimals:  net.decimals,
				Chain:     chain,
				Liquidity: liquidity,
			})
		}
		emit(balance.Liquid.Add(balance.Rewards), entity.LiquidityLiquid)
		emit(balance.Delegated, entity.LiquidityStaked)
		emit(balance.Unbonding, entity.LiquidityUnbonding)
	}

	return balances, errors.Join(errs...)
}
