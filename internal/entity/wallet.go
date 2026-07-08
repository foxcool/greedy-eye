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

// WalletSyncer fetches token balances for a blockchain wallet across one or more chains.
//
// The implementation owns all provider-specific mechanics: chain discovery, per-chain
// fan-out, and native-vs-token balance retrieval. Callers receive a flat, normalized list
// of balances and remain unaware of the underlying provider's API shape.
type WalletSyncer interface {
	// SyncWallet returns native and token balances for the address across the given chains.
	// When chains is empty, the implementation auto-discovers the chains with activity.
	// A non-nil error may accompany a partial result: balances gathered before the failure
	// are still returned so the caller can surface per-chain errors without losing data.
	SyncWallet(ctx context.Context, address string, chains []string) ([]WalletBalance, error)
}

// ExchangeBalance represents a single asset balance held on a centralized exchange.
type ExchangeBalance struct {
	Symbol   string // asset ticker as reported by the exchange (e.g. "BTC")
	Amount   string // raw integer string scaled by Decimals (free + locked)
	Decimals int
}

// ExchangeSyncer fetches account balances from a centralized exchange using the
// credentials baked into the syncer at construction time (per-account, resolved
// from stored API keys). Unlike WalletSyncer there is no address: the API key
// identifies the account.
type ExchangeSyncer interface {
	// SyncExchange returns the non-zero spot balances of the credentialed account.
	SyncExchange(ctx context.Context) ([]ExchangeBalance, error)
}
