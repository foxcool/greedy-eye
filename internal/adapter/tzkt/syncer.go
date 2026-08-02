package tzkt

import (
	"context"
	"fmt"
	"regexp"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// SupportedChains returns the chains this adapter serves, for registration in
// the chain-keyed syncer registry.
func SupportedChains() []string { return []string{Chain} }

// tezosAddress matches the Tezos forms: implicit accounts (tz1 ed25519, tz2
// secp256k1, tz3 p256) and originated contracts (KT1), all base58 over a
// 3-character prefix and a fixed 33-character body.
var tezosAddress = regexp.MustCompile(`^(tz[1-3]|KT1)[1-9A-HJ-NP-Za-km-z]{33}$`)

// HandlesAddress reports whether an address is a Tezos one, routing accounts
// that name no chain to this adapter.
func HandlesAddress(address string) bool {
	return tezosAddress.MatchString(address)
}

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet returns the XTZ balance of the address, staked value included.
//
// Tezos is a single chain, so the chains argument only guards against being
// routed the wrong ecosystem. FA1.2/FA2 tokens are not covered: this account
// holds tez only, and a token catalogue brings the same scam-filtering problem
// as every other chain (personal-6yn).
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	for _, c := range chains {
		if c != Chain {
			return nil, fmt.Errorf("tzkt: unsupported chain %q", c)
		}
	}

	account, err := a.client.GetAccount(ctx, address)
	if err != nil {
		return nil, err
	}

	total := account.Total()
	if total.IsZero() {
		return nil, nil // an address that holds nothing yields no position
	}

	// TzKT reports mutez, which is already the raw integer form.
	balance := func(amount decimal.Decimal, liquidity entity.Liquidity) entity.WalletBalance {
		return entity.WalletBalance{
			Symbol:    nativeSymbol,
			Name:      nativeName,
			Amount:    amount.BigInt().String(),
			Decimals:  nativeDecimals,
			Chain:     Chain,
			Liquidity: liquidity,
		}
	}

	// Staked and Unstaked sit INSIDE Balance (see the Account doc), so the
	// spendable part is what remains after both are taken out — never a sum.
	spendable := total.Sub(account.Staked).Sub(account.Unstaked)
	if spendable.IsNegative() {
		// The two frozen figures overlap, which contradicts what TzKT
		// documents. Rather than invent a split, report the position whole and
		// unclassified: an unknown liquidity is a gap, a wrong one is a lie
		// about how much can be spent.
		return []entity.WalletBalance{balance(total, entity.LiquidityUnknown)}, nil
	}

	var out []entity.WalletBalance
	if !spendable.IsZero() {
		out = append(out, balance(spendable, entity.LiquidityLiquid))
	}
	if !account.Staked.IsZero() {
		out = append(out, balance(account.Staked, entity.LiquidityStaked))
	}
	// Unstaked is frozen until finalization, then spendable without any further
	// decision — the unbonding state, not an open-ended lock.
	if !account.Unstaked.IsZero() {
		out = append(out, balance(account.Unstaked, entity.LiquidityUnbonding))
	}
	return out, nil
}
