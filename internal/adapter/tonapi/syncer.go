package tonapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// SupportedChains returns the chains this adapter serves, for registration in
// the chain-keyed syncer registry.
func SupportedChains() []string { return []string{Chain} }

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet returns the native TON balance plus jetton balances, including
// liquid staking positions (tsTON is an ordinary jetton).
//
// TON is a single chain, so the chains argument only guards against being
// routed the wrong ecosystem. A failure fetching jettons still returns the
// native balance: losing the whole account over a jetton listing error would
// hide the largest position.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	for _, c := range chains {
		if c != Chain {
			return nil, fmt.Errorf("tonapi: unsupported chain %q", c)
		}
	}

	var (
		balances []entity.WalletBalance
		errs     []error
	)

	native, err := a.client.GetAccountBalance(ctx, address)
	if err != nil {
		errs = append(errs, fmt.Errorf("native balance: %w", err))
	} else if native != "0" && native != "" {
		balances = append(balances, entity.WalletBalance{
			Symbol:   nativeSymbol,
			Name:     nativeName,
			Amount:   native,
			Decimals: nativeDecimals,
		})
	}

	jettons, err := a.client.GetJettons(ctx, address)
	if err != nil {
		errs = append(errs, fmt.Errorf("jettons: %w", err))
		return balances, errors.Join(errs...)
	}

	for _, j := range jettons {
		if isScam(j) || j.Amount == "" || j.Amount == "0" {
			continue
		}
		balances = append(balances, entity.WalletBalance{
			Symbol:   j.Symbol,
			Name:     j.Name,
			Amount:   j.Amount,
			Decimals: j.Decimals,
			// The jetton master doubles as the contract address, so price
			// providers can look the token up the same way as an ERC-20.
			ContractAddress: j.Address,
		})
	}

	return balances, errors.Join(errs...)
}

// isScam drops what tonapi itself flags. This is a floor, not a filter: TON's
// airdrop spam routinely carries verification "none" (observed live:
// "TONNEL 💎 Unlock at TONNEL.ME" — a phishing domain in the ticker, neither
// blacklisted nor scam-flagged), and "none" is also the normal state of every
// legitimate small jetton, so it cannot be dropped wholesale.
//
// Catalogue-wide scoring is personal-6yn; until it lands, TON jettons will
// bring some junk in with them.
func isScam(j Jetton) bool {
	return j.IsScam || j.Verification == "blacklist"
}
