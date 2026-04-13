package entity

import "context"

// WalletBalance represents a single token balance returned by a wallet syncer.
type WalletBalance struct {
	Symbol          string
	Name            string
	Amount          string // raw integer string (no decimals applied)
	Decimals        int
	ContractAddress string // EVM token contract address; empty for native coins
}

// WalletSyncer fetches token balances for a blockchain wallet.
type WalletSyncer interface {
	// GetWalletTokenBalances returns ERC-20 / SPL / etc. token balances.
	GetWalletTokenBalances(ctx context.Context, chain, address string) ([]WalletBalance, error)
	// GetNativeBalance returns the native coin balance (ETH, MATIC, BNB, …).
	// Returns nil without error when the balance is zero or chain is unsupported.
	GetNativeBalance(ctx context.Context, chain, address string) (*WalletBalance, error)
	// GetActiveChains returns the chains where this address has had activity.
	// Used for auto-discovery when no explicit chain is configured.
	GetActiveChains(ctx context.Context, address string) ([]string, error)
}
