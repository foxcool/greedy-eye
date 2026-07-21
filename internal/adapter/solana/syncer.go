package solana

import (
	"context"
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/base58"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// SupportedChains returns the chains this adapter serves, for registration in
// the chain-keyed syncer registry.
func SupportedChains() []string { return []string{Chain} }

// pubkeyLen is the size of a Solana address: a bare 32-byte ed25519 public key,
// carrying neither a network prefix nor a checksum.
const pubkeyLen = 32

// HandlesAddress reports whether an address is a Solana one, routing accounts
// that name no chain to this adapter.
//
// The check is structural rather than by length because Solana shares its
// alphabet with SS58 and sits only two characters short of it (43-44 against
// 46-48). Decoding to exactly 32 bytes cannot collide with SS58, which always
// decodes to 35 or 36.
func HandlesAddress(address string) bool {
	decoded, err := base58.Decode(address)
	if err != nil {
		return false
	}
	return len(decoded) == pubkeyLen
}

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet returns the native SOL balance plus SPL token balances.
//
// Solana is a single chain, so the chains argument only guards against being
// routed the wrong ecosystem. A failure listing tokens still returns the native
// balance: losing the whole account over a token listing error would hide the
// largest position.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	for _, c := range chains {
		if c != Chain {
			return nil, fmt.Errorf("solana: unsupported chain %q", c)
		}
	}

	var (
		balances []entity.WalletBalance
		errs     []error
	)

	lamports, err := a.client.GetBalance(ctx, address)
	if err != nil {
		errs = append(errs, fmt.Errorf("native balance: %w", err))
	} else if lamports != "" && lamports != "0" {
		balances = append(balances, entity.WalletBalance{
			Symbol:   nativeSymbol,
			Name:     nativeName,
			Amount:   lamports,
			Decimals: nativeDecimals,
			Chain:    Chain,
		})
	}

	accounts, err := a.client.GetTokenAccounts(ctx, address)
	if err != nil {
		errs = append(errs, fmt.Errorf("token accounts: %w", err))
		return balances, errors.Join(errs...)
	}

	// Empty token accounts are the norm on Solana — the account survives the
	// balance going to zero — so they are dropped before the metadata call
	// rather than after, keeping the batch to what is actually held.
	held := make([]TokenAccount, 0, len(accounts))
	mints := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		if acc.Amount == "" || acc.Amount == "0" {
			continue
		}
		held = append(held, acc)
		mints = append(mints, acc.Mint)
	}

	meta, err := a.client.GetAssetMetadata(ctx, mints)
	if err != nil {
		// Without symbols the positions cannot be labelled or priced, so they
		// are dropped rather than emitted as mint addresses. Native SOL stands.
		errs = append(errs, fmt.Errorf("asset metadata: %w", err))
		return balances, errors.Join(errs...)
	}

	for _, acc := range held {
		m, ok := meta[acc.Mint]
		if !ok || isJunk(acc, m) {
			continue
		}
		balances = append(balances, entity.WalletBalance{
			Symbol:   m.Symbol,
			Name:     m.Name,
			Amount:   acc.Amount,
			Decimals: acc.Decimals,
			// The mint doubles as the contract address, so price providers can
			// look the token up the same way as an ERC-20.
			ContractAddress: acc.Mint,
			Chain:           Chain,
		})
	}

	return balances, errors.Join(errs...)
}

// isJunk drops what cannot be a priceable fungible position — a structural
// floor, not a scam filter. It removes shapes that are not a balance at all;
// judging whether a plausibly-shaped token is a scam clone or an impersonation
// is the scoring layer's job (scam-filtering), which lets such a token sync as
// its own quarantined asset rather than vanish here.
//
// The rules were written against a live wallet whose three SPL positions were
// all spam: an airdrop lure naming a phishing domain, a mint calling itself
// "NFT", and "Rауdium аlрhа рrоgrаm" in Cyrillic lookalikes. The domain and
// Cyrillic ones now flow through to scoring (which condemns them by verdict);
// only the structurally-impossible remain dropped here.
func isJunk(acc TokenAccount, m AssetMeta) bool {
	switch {
	case m.Burnt, m.Symbol == "":
		return true

	// Indivisible units are NFTs and airdrop lures, not balances. A fungible
	// token that could hold value has decimals; dropping the rest costs the
	// occasional oddity and removes most of the spam.
	case acc.Decimals == 0:
		return true
	}
	return false
}
