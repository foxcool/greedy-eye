package subscan

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

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
		row := func(amount decimal.Decimal, liquidity entity.Liquidity) entity.WalletBalance {
			return entity.WalletBalance{
				Symbol:    account.Symbol,
				Name:      net.name,
				Amount:    amount.BigInt().String(),
				Decimals:  int(account.Decimals),
				Chain:     chain,
				Liquidity: liquidity,
			}
		}
		balances = append(balances, splitLiquidity(account, row)...)
	}

	return balances, errors.Join(errs...)
}

// splitLiquidity partitions an account's balance into spendable and frozen
// parts, or declines to.
//
// Reserved, Bonded and Unbonding are each a subset of Balance, and Subscan says
// nothing about whether they overlap each other. Measured on the dev accounts
// 2026-08-02, they do — decisively. On Kusama Asset Hub the same 5.637369256383
// KSM is reported THREE times, as reserved, as bonded and as lock, against a
// balance of 6.031593575767. Subtracting reserved and bonded both would take
// 11.27 KSM out of 6.03 and drive the position negative. On Hydration the same
// hold appears as bonded and lock with reserved at zero.
//
// So the split is only made where the parts provably reconcile:
//
//   - reserved == bonded — the staking hold reported twice (Asset Hub). The
//     frozen part is bonded plus unbonding, counted once.
//   - reserved == 0 — a lock on free balance, no hold (relay chains, Hydration).
//     Same arithmetic.
//
// Anything else is left whole and unclassified. A reserve that is neither zero
// nor equal to bonded could be a deposit disjoint from the staking lock or the
// same planck seen through a different field, and the two answers differ in the
// direction that matters: guessing wrong here overstates what can be spent,
// which is exactly the lie the runway figure must not be told. An unknown
// liquidity is a gap; a wrong one is a false claim about available money.
func splitLiquidity(a Account, row func(decimal.Decimal, entity.Liquidity) entity.WalletBalance) []entity.WalletBalance {
	total := a.Total()
	unclassified := []entity.WalletBalance{row(total, entity.LiquidityUnknown)}

	if !a.Reserved.IsZero() && !a.Reserved.Equal(a.Bonded) {
		return unclassified
	}

	frozen := a.Bonded.Add(a.Unbonding)
	spendable := total.Sub(frozen)
	if spendable.IsNegative() {
		// Bonded and unbonding overlap each other, which the reconciliation
		// above cannot see. Same rule: report the position whole.
		return unclassified
	}

	var out []entity.WalletBalance
	if !spendable.IsZero() {
		out = append(out, row(spendable, entity.LiquidityLiquid))
	}
	if !a.Bonded.IsZero() {
		out = append(out, row(a.Bonded, entity.LiquidityStaked))
	}
	// Unbonding is on its way out with no further decision to make: it becomes
	// spendable when the era ends, which is the unbonding state, not a lock.
	if !a.Unbonding.IsZero() {
		out = append(out, row(a.Unbonding, entity.LiquidityUnbonding))
	}
	return out
}
