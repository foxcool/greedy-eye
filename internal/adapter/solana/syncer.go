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
		})
	}

	return balances, errors.Join(errs...)
}

// isJunk drops what cannot be a priceable fungible position. This is a floor,
// not a filter: SPL airdrop spam is as prolific as its EVM counterpart and
// routinely carries a plausible symbol, which no structural rule can catch.
//
// Catalogue-wide scoring is personal-6yn; until it lands, SPL tokens will bring
// some junk in with them.
func isJunk(acc TokenAccount, m AssetMeta) bool {
	if m.Burnt || m.Symbol == "" {
		return true
	}
	// A single indivisible unit is the NFT shape, not a balance.
	return acc.Decimals == 0 && acc.Amount == "1"
}
