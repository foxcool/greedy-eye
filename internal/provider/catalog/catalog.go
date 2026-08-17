// Package catalog describes what an external provider needs before anyone can
// use it: which slug names it, whether it wants a key, which chains it reads,
// and what plans it is metered under.
//
// It is deliberately separate from the registry that builds clients. A
// description carries no adapter code, so the account form can be served by a
// handler that never links a single adapter — while the description itself
// stays beside the factory it describes (internal/provider), because a
// catalogue kept anywhere else drifts from the code it claims to describe.
// That drift is the whole reason this package exists: before it, the only
// statement of "which providers are there" lived in a table in main.go, and a
// form offering a choice had to hardcode a second copy of it.
package catalog

import "github.com/foxcool/greedy-eye/internal/adapter/ratelimit"

// Kind is a service a provider performs. A provider may do several: Binance
// both syncs an exchange account and quotes prices.
type Kind string

const (
	// KindPrice quotes assets.
	KindPrice Kind = "price"
	// KindWallet reads balances off a chain.
	KindWallet Kind = "wallet"
	// KindExchange syncs positions held at a venue.
	KindExchange Kind = "exchange"
)

// Field is an entry in accounts.data the provider needs beyond the usual key
// and secret.
//
// It exists because "credential" is not always an API key. T-Invest needs a
// trust anchor: a PEM certificate the operator decides to trust, which no
// generic key field can hold and no service configuration should carry.
type Field struct {
	// Key is the accounts.data key, e.g. "root_ca".
	Key string
	// Title names the field for a person.
	Title string
	// Help says what happens without it, in one sentence.
	Help string
	// Required marks a field the provider cannot work without.
	Required bool
	// Multiline asks for a textarea rather than an input: a PEM block is not
	// a line.
	Multiline bool
}

// Tier is a plan the provider meters a credential under, with the limits this
// instance applies by default.
//
// Named after the value that goes into accounts.data["tier"], so what a person
// picks in a form is what the rate limiter looks up. An empty Name is the
// provider's free keyed plan.
type Tier struct {
	Name  string
	Limit ratelimit.Limit
}

// Descriptor is everything that can be said about a provider without building
// a client from it.
type Descriptor struct {
	// Slug is the value of accounts.data["provider"] — the identity the
	// resolver routes on.
	Slug string
	// Title names the provider for a person.
	Title string
	// Kinds are the services this provider performs.
	Kinds []Kind
	// Capabilities are the account capabilities that make this provider
	// reachable. Derived from Kinds so a form can pre-select them instead of
	// asking a person to know the mapping.
	Capabilities []string
	// NeedsAPIKey reports whether the provider is unusable without a key.
	NeedsAPIKey bool
	// NeedsAPISecret reports whether it also needs a secret (exchange APIs
	// sign requests; read-only price feeds do not).
	NeedsAPISecret bool
	// Keyless reports that the provider answers with no account at all and is
	// registered by default. An account naming its slug still wins — that is
	// how a free feed gets throttled after an enforcement notice, or given a
	// smaller share of a shared plan.
	Keyless bool
	// Chains this provider reads, for wallet providers. Empty elsewhere.
	Chains []string
	// Fields are extra accounts.data entries beyond api_key/api_secret.
	Fields []Field
	// Tiers are the plans the provider is metered under.
	Tiers []Tier
}
