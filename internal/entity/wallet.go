package entity

import "context"

// WalletBalance represents a single token balance returned by a wallet syncer.
type WalletBalance struct {
	Symbol   string
	Name     string
	Amount   string // raw integer string (no decimals applied)
	Decimals int
}

// WalletSyncer fetches token balances for a blockchain wallet.
type WalletSyncer interface {
	GetWalletTokenBalances(ctx context.Context, chain, address string) ([]WalletBalance, error)
}
