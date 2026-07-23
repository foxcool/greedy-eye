package entity

import "context"

// WalletBalance represents a single token balance returned by a wallet syncer.
type WalletBalance struct {
	Symbol   string
	Name     string
	Amount   string // raw integer string (no decimals applied)
	Decimals int
	// ContractAddress is the token contract/mint address; empty for native coins.
	ContractAddress string
	// Chain identifies the network this balance is on ("eth", "solana",
	// "polkadot", ...). Both native coins and tokens carry it. It namespaces the
	// contract identity (OnchainSource) so the same address on two chains
	// resolves to two distinct assets.
	Chain string
	// ProviderSpam is a source's own spam flag where reported (moralis
	// possible_spam); nil when the provider does not report it. Fed to the
	// identity scorer at sync intake.
	ProviderSpam *bool
	// ContractVerified is a source's contract-verification bit where reported;
	// nil when unreported (native coins, non-EVM chains).
	ContractVerified *bool
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
