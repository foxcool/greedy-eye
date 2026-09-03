package entity

import (
	"context"
	"time"
)

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
	// Liquidity says how reachable this part of the balance is. Empty means the
	// adapter cannot state it, which is different from "liquid": a chain that
	// reports one opaque total must not have it read as spendable. An adapter
	// that CAN partition emits one WalletBalance per pool, and the pools must
	// not overlap — double-counting a staked position is the trap both the
	// Substrate and Tezos clients document.
	Liquidity Liquidity
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

// BrokerPosition is one line of a broker account's portfolio.
//
// A third shape was needed because ExchangeBalance is Symbol/Amount/Decimals
// and a broker line loses three things in that shape, each of which the
// valuation needs: the FIGI that is its only honest identity, the currency of
// THIS row (one response mixes roubles, dollars and euros), and the instrument
// type that decides whether it can be valued at all.
type BrokerPosition struct {
	// Ref is the provider's instrument id — a FIGI — and the identity this
	// position binds by. The broker's ticker field frequently holds an ISIN
	// instead of a ticker, so matching on the ticker attaches a position to
	// whatever asset shares the string.
	Ref string
	// Symbol is the ticker for a human to read and for a new asset row to carry.
	// Never an identity.
	Symbol string
	Name   string
	Type   AssetType
	// Market is the venue the instrument lists on, as spelled in assets.market.
	// Empty means the adapter could not tell — the caller decides what to do
	// with that rather than the adapter guessing.
	Market string
	// Currency is the row's own quote currency, lowercase as the broker spells
	// it. It belongs to the row and never to the provider.
	Currency string

	Amount   string // raw integer string scaled by Decimals
	Decimals int
	// Liquidity partitions the position. A broker that blocks part of a holding
	// yields two BrokerPositions over the same Ref, one liquid and one locked,
	// which must sum to the reported quantity — the pools do not overlap, the
	// same rule the Cosmos and Substrate adapters document.
	Liquidity Liquidity
}

// BrokerSyncer reads the positions of ONE account at a broker.
//
// One account, not all of them: a single API token reaches several, and each is
// a separate account in our model. Merging them would collapse two holdings of
// the same share into one row and, worse, make a transfer between accounts
// invisible — the sum does not move, so for anything watching, the event never
// happened.
type BrokerSyncer interface {
	// SyncBroker returns the positions of the account named at construction.
	// Positions the syncer could not turn into a holding are reported in
	// BrokerSkips rather than dropped: a sum has to disclose what is not in it.
	SyncBroker(ctx context.Context) ([]BrokerPosition, BrokerSkips, error)
}

// BrokerAccountDataKey names, in accounts.data, which of the broker's own
// accounts an account here speaks for.
//
// It lives beside the Account it is a key of, because two packages read it for
// opposite reasons — the provider registry to build a syncer for one account,
// the portfolio service to decide which accounts already exist — and a key
// spelled twice is a key that can be renamed once.
const BrokerAccountDataKey = "broker_account_id"

// BrokerAccountRef is one account a broker token reaches.
//
// It exists because a token is not an account: one reaches several, and until
// something asked, the only way to learn their ids was the broker's own app.
// An operator typing an id from a phone screen into a text field is the step
// this removes.
type BrokerAccountRef struct {
	// ID is the broker's own account id, the value data["broker_account_id"]
	// carries.
	ID string
	// Name is the broker's label for it, for a human choosing between three
	// rows that are otherwise identical numbers.
	Name string
	// Syncable is the ADAPTER's judgement, not the caller's: which statuses and
	// kinds of account hold positions worth reading is provider knowledge, the
	// same rule that puts venue resolution in the adapter. A savings pot holding
	// 2.67 roubles and a closed account are both real answers and neither is a
	// portfolio.
	Syncable bool
	// NotSyncableReason names why, in words meant for a person. Empty when
	// Syncable.
	NotSyncableReason string
	// ReadOnly reports that the token cannot trade on this account. The whole
	// decision to point development at the live broker rests on that being
	// true, so it is carried out where a human can see it rather than assumed.
	ReadOnly bool
}

// BrokerAccountLister lists the accounts one broker token reaches.
//
// Separate from BrokerSyncer because a syncer speaks for ONE account and this
// speaks for the token: the listing is what tells the system which syncers to
// build in the first place.
type BrokerAccountLister interface {
	ListBrokerAccounts(ctx context.Context) ([]BrokerAccountRef, error)
}

// BrokerSkips counts what a broker sync did not bring back, by reason.
//
// Counts rather than a list because this is the sync's own report, read by an
// operator deciding whether to look further; the positions themselves are
// reachable from the broker. A zero-valued BrokerSkips means the response was
// understood in full.
type BrokerSkips struct {
	// UnknownInstrument counts positions whose id the instrument catalogue does
	// not carry — delisted paper, or a type this adapter does not load.
	UnknownInstrument int
	// UnknownMarket counts positions whose instrument settles on a venue this
	// system has no market for.
	UnknownMarket int
	// DefaultedMarket counts positions given a market inferred from the row's
	// currency because the catalogue offered none. These ARE returned, and the
	// count exists so the guess is never silent.
	DefaultedMarket int
	// Unparsable counts positions whose quantity or shape could not be read.
	Unparsable int
}

// Total is how many positions the sync could not account for exactly.
// DefaultedMarket is excluded: those positions were returned, only on a guessed
// market.
func (s BrokerSkips) Total() int {
	return s.UnknownInstrument + s.UnknownMarket + s.Unparsable
}

// SyncDeferral is one account's standing with the balance sweep: when its
// holdings were last confirmed, how many consecutive syncs left them no
// fresher, and when the sweep may look at it again.
//
// It is a fact about scheduling, not about the account's contents. An account
// can be deferred and perfectly configured — a provider outage is the ordinary
// cause — which is why the entry carries the miss count rather than a verdict.
type SyncDeferral struct {
	AccountID     string
	AccountName   string
	LastSyncedAt  *time.Time
	Misses        int
	NextAttemptAt time.Time
}
