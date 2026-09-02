// Package credentials resolves provider clients from account credentials
// stored in the database. Resolution order per the account-capabilities
// design: the user's own account with the capability → an admin-shared system
// account → for unattended work, the sole operator's own.
//
// Credentials do not come from configuration. A key names a plan, a plan names
// money, and both belong to whoever owns the account — not to the service that
// happens to run beside them (personal-s05). Sources needing no credential at
// all are the exception and are registered directly; see KeylessPriceProviders.
package credentials

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
)

// DataProviderKey is the accounts.data key naming the external provider the
// account credentials belong to (e.g. "moralis", "coingecko", "binance").
// Accounts without it don't participate in credential resolution.
const DataProviderKey = "provider"

// AccountSource lists accounts able to satisfy a capability.
type AccountSource interface {
	ListUserAccountsByCapability(ctx context.Context, userID string, capability entity.AccountCapability) ([]*entity.Account, error)
	ListSystemAccountsByCapability(ctx context.Context, capability entity.AccountCapability) ([]*entity.Account, error)
	// ListCapabilityOwners returns the users holding an account with the
	// capability. Unattended work reads it to tell a single-operator instance
	// from a shared one.
	ListCapabilityOwners(ctx context.Context, capability entity.AccountCapability) ([]string, error)
}

// WalletSyncerFactory builds a wallet syncer from account credentials.
type WalletSyncerFactory func(a *entity.Account) (entity.WalletSyncer, error)

// WalletProvider couples a syncer factory with the chains that provider covers
// and the address shape it understands. Routing by chain is what lets EVM
// (Moralis) and non-EVM ecosystems coexist: an account naming chain "polkadot"
// must never reach an EVM-only syncer.
type WalletProvider struct {
	Factory WalletSyncerFactory

	// Chains this provider can sync. An empty list marks a catch-all provider
	// usable for any chain.
	Chains []string

	// HandlesAddress reports whether an address belongs to this provider's
	// ecosystem. It resolves auto-discovery — an account naming no chain —
	// where there is nothing but the address to route on. Providers that
	// leave it nil cannot serve auto-discovery.
	//
	// Discovery itself stays inside the syncer: each one sweeps the chains it
	// knows, exactly as the EVM syncer already probes its candidate list.
	HandlesAddress func(address string) bool
}

// matches reports whether the provider should serve this request.
//
// With chains named, it must cover every one of them: partial coverage would
// silently drop the balances of the chain it cannot reach. With none named the
// account wants discovery, which is decided by address shape.
func (p WalletProvider) matches(address string, chains []string) bool {
	if len(chains) == 0 {
		if len(p.Chains) == 0 {
			return true // catch-all
		}
		return p.HandlesAddress != nil && p.HandlesAddress(address)
	}
	if len(p.Chains) == 0 {
		return true
	}
	for _, want := range chains {
		if !slices.Contains(p.Chains, want) {
			return false
		}
	}
	return true
}

// ExchangeSyncerFactory builds an exchange syncer from account credentials.
type ExchangeSyncerFactory func(a *entity.Account) (entity.ExchangeSyncer, error)

// BrokerSyncerFactory builds a broker syncer from account credentials.
//
// One factory call reads ONE account at the broker. A single token reaches
// several, and each of those is a separate account here: which one this syncer
// speaks for is named by the account itself, in data["broker_account_id"].
type BrokerSyncerFactory func(a *entity.Account) (entity.BrokerSyncer, error)

// PriceProviderFactory builds a price provider from account credentials.
type PriceProviderFactory func(a *entity.Account) (marketdata.PriceProvider, error)

// Config wires a Resolver: per-provider factories plus env-configured fallbacks.
type Config struct {
	Source          AccountSource
	WalletSyncers   map[string]WalletProvider        // keyed by provider slug
	ExchangeSyncers map[string]ExchangeSyncerFactory // keyed by provider slug
	BrokerSyncers   map[string]BrokerSyncerFactory   // keyed by provider slug
	PriceProviders  map[string]PriceProviderFactory  // keyed by provider slug

	// KeylessWalletSyncers are chain readers that need no credential: public
	// block explorers and RPC endpoints. Registered unconditionally for the same
	// reason as the price feeds below — there is nothing to configure and
	// nothing to keep secret.
	//
	// It matters more here than it looks. A wallet syncer is chosen from the
	// accounts carrying onchain_lookup, so before this a fresh instance synced
	// NOTHING until somebody hand-created a service row per ecosystem. The
	// wallet account holds the address; it never names the reader.
	//
	// An account naming the same slug still wins, which is how one of these gets
	// throttled or pointed elsewhere.
	KeylessWalletSyncers map[string]WalletProvider // may be empty

	// KeylessPriceProviders are sources that need no credential at all: a public
	// feed anyone may read. They are registered unconditionally, because there
	// is nothing to configure and nothing to keep secret — without the CBR rate
	// a rouble-quoted instrument has a price and still contributes nothing to a
	// USD total.
	//
	// An account naming the same provider still wins: that is how someone
	// throttles a free feed after an enforcement notice, or gives one
	// deployment a smaller share. Registering by default and overriding by
	// account beats seeding a row — accounts.user_id is NOT NULL, and a fresh
	// instance has no user to own one.
	KeylessPriceProviders map[string]marketdata.PriceProvider // may be empty
	Log                   *slog.Logger
}

// Resolver resolves per-account provider clients with an in-memory cache
// invalidated by the account's updated_at timestamp.
type Resolver struct {
	cfg Config

	mu    sync.Mutex
	cache map[string]cacheEntry // "<kind>:<account_id>" → built client
}

type cacheEntry struct {
	updatedAt time.Time
	client    any
}

func NewResolver(cfg Config) *Resolver {
	return &Resolver{cfg: cfg, cache: map[string]cacheEntry{}}
}

// Skipped names an account unattended work could have used and did not, with
// the reason it was passed over. It exists because the failure it describes has
// no other surface: a request made BY someone resolves that person's own
// accounts and works, so a provider missing only from the scheduler's view is
// invisible everywhere a human looks.
type Skipped struct {
	// Provider is the slug the account names, empty when the skip is about the
	// instance rather than one account.
	Provider  string
	AccountID string
	Reason    string
}

// accountsFor returns candidate accounts for the capability in resolution
// order: the user's own accounts first, then system-shared ones. It also
// returns what it passed over, so a caller can say so out loud.
func (r *Resolver) accountsFor(ctx context.Context, userID string, capability entity.AccountCapability) ([]*entity.Account, []Skipped, error) {
	var candidates []*entity.Account
	if userID != "" {
		own, err := r.cfg.Source.ListUserAccountsByCapability(ctx, userID, capability)
		if err != nil {
			return nil, nil, fmt.Errorf("list user accounts: %w", err)
		}
		candidates = append(candidates, own...)
	}
	system, err := r.cfg.Source.ListSystemAccountsByCapability(ctx, capability)
	if err != nil {
		return nil, nil, fmt.Errorf("list system accounts: %w", err)
	}
	candidates = append(candidates, system...)

	if userID != "" {
		return candidates, nil, nil
	}

	owned, skipped, err := r.soleOperatorAccounts(ctx, capability)
	if err != nil {
		return nil, nil, err
	}
	candidates = append(candidates, unclaimedProviders(candidates, owned)...)
	return candidates, skipped, nil
}

// unclaimedProviders returns the operator's accounts for the providers no
// candidate covers.
//
// The fallback used to fire only when the candidate list came back empty, which
// answers "this instance has no system account at all" — not "it has one for
// some other provider", which is the far more likely state of a real instance,
// because scopes are granted one at a time as each account is created.
//
// Prod 2026-08-17 held a market-data system scope on coingecko, subscan and
// helius, so the list was never empty and binance — the one live crypto source
// left after the CoinGecko plan burned out — was simply absent from it. The
// hourly sweep refreshed no crypto price for six days while every manual fetch
// succeeded, because those carry a user id. Falling back per capability is what
// made partial coverage silent; the rule is per provider.
//
// An explicitly scoped account still wins its own slug: the fallback exists for
// instances with no way to grant a scope, not to overrule one that was granted.
func unclaimedProviders(candidates, owned []*entity.Account) []*entity.Account {
	covered := make(map[string]bool, len(candidates))
	for _, a := range candidates {
		covered[a.Data[DataProviderKey]] = true
	}

	var extra []*entity.Account
	for _, a := range owned {
		if covered[a.Data[DataProviderKey]] {
			continue
		}
		extra = append(extra, a)
	}
	return extra
}

// soleOperatorAccounts returns the credentials of the one person who holds them,
// for work that runs with nobody in context.
//
// Unattended jobs see only accounts carrying system_scopes, and setting those
// requires the admin role — which no RPC grants, so on a self-hosted instance
// there is no supported way to reach it. The result was a deadlock with a
// silent failure mode: prod 2026-07-25 had fresh keys sitting in accounts while
// the sweep quietly went on using stale environment ones.
//
// On an instance with a single operator the distinction between "their account"
// and "the system's account" is ceremony, and it is the ceremony that produced
// the deadlock. With several credential holders it stops being ceremony — one
// person's plan must not be spent on everyone's behalf — so the fallback
// declines, and says so rather than leaving an operator to wonder why the sweep
// found nothing.
func (r *Resolver) soleOperatorAccounts(ctx context.Context, capability entity.AccountCapability) ([]*entity.Account, []Skipped, error) {
	owners, err := r.cfg.Source.ListCapabilityOwners(ctx, capability)
	if err != nil {
		return nil, nil, fmt.Errorf("list capability owners: %w", err)
	}
	if len(owners) != 1 {
		if len(owners) <= 1 {
			return nil, nil, nil
		}
		if r.cfg.Log != nil {
			r.cfg.Log.Warn("unattended work will not choose between operators",
				slog.String("capability", string(capability)),
				slog.Int("credential_holders", len(owners)))
		}
		return nil, []Skipped{{Reason: fmt.Sprintf(
			"%d operators hold %s credentials and no system scope names one",
			len(owners), capability)}}, nil
	}

	own, err := r.cfg.Source.ListUserAccountsByCapability(ctx, owners[0], capability)
	if err != nil {
		return nil, nil, fmt.Errorf("list sole operator accounts: %w", err)
	}
	return own, nil, nil
}

// clientFor returns the cached client for the account, rebuilding it when the
// account has been updated since it was cached.
func (r *Resolver) clientFor(kind string, a *entity.Account, build func() (any, error)) (any, error) {
	key := kind + ":" + a.ID

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.cache[key]; ok && entry.updatedAt.Equal(a.UpdatedAt) {
		return entry.client, nil
	}

	client, err := build()
	if err != nil {
		return nil, err
	}
	r.cache[key] = cacheEntry{updatedAt: a.UpdatedAt, client: client}
	return client, nil
}

// WalletSyncerFor resolves a wallet syncer for the user (onchain_lookup
// capability) able to sync every chain in chains. An empty chains list means
// auto-discovery, resolved by the address's shape. Returns the env-configured
// syncer (possibly nil) when no account-based credentials match.
func (r *Resolver) WalletSyncerFor(ctx context.Context, userID, address string, chains []string) (entity.WalletSyncer, error) {
	candidates, _, err := r.accountsFor(ctx, userID, entity.CapabilityOnchainLookup)
	if err != nil {
		return nil, err
	}

	for _, a := range candidates {
		provider, ok := r.cfg.WalletSyncers[a.Data[DataProviderKey]]
		if !ok || !provider.matches(address, chains) {
			continue
		}
		client, err := r.clientFor("wallet_syncer", a, func() (any, error) { return provider.Factory(a) })
		if err != nil {
			return nil, fmt.Errorf("build wallet syncer from account %s: %w", a.ID, err)
		}
		return client.(entity.WalletSyncer), nil
	}

	// Nobody's account claims this address, so fall back to the readers that
	// need no credential. Routing still decides: a keyless reader serves its own
	// ecosystem and no other.
	for slug, provider := range r.cfg.KeylessWalletSyncers {
		if !provider.matches(address, chains) {
			continue
		}
		syncer, err := provider.Factory(nil)
		if err != nil {
			return nil, fmt.Errorf("build keyless wallet syncer %s: %w", slug, err)
		}
		return syncer, nil
	}

	// Still nothing, and that is the answer. There is no catch-all: one would
	// reach for an EVM syncer on a Substrate address and report an empty wallet,
	// which reads as "you hold nothing" rather than as an error.
	return nil, nil
}

// ExchangeSyncerForAccount builds an exchange syncer from the account's own
// stored credentials (the account being synced holds the API key — there is no
// user→system fallback as with wallets). Returns nil when no adapter is
// registered for the account's provider slug.
func (r *Resolver) ExchangeSyncerForAccount(a *entity.Account) (entity.ExchangeSyncer, error) {
	factory, ok := r.cfg.ExchangeSyncers[a.Data[DataProviderKey]]
	if !ok {
		return nil, nil
	}
	client, err := r.clientFor("exchange_syncer", a, func() (any, error) { return factory(a) })
	if err != nil {
		return nil, fmt.Errorf("build exchange syncer from account %s: %w", a.ID, err)
	}
	return client.(entity.ExchangeSyncer), nil
}

// BrokerSyncerForAccount builds a broker syncer from the account's own stored
// credentials. Same shape as ExchangeSyncerForAccount and for the same reason:
// the account being synced holds the token, so there is no user->system
// fallback. Returns nil when no adapter is registered for the account's
// provider slug.
func (r *Resolver) BrokerSyncerForAccount(a *entity.Account) (entity.BrokerSyncer, error) {
	factory, ok := r.cfg.BrokerSyncers[a.Data[DataProviderKey]]
	if !ok {
		return nil, nil
	}
	client, err := r.clientFor("broker_syncer", a, func() (any, error) { return factory(a) })
	if err != nil {
		return nil, fmt.Errorf("build broker syncer from account %s: %w", a.ID, err)
	}
	return client.(entity.BrokerSyncer), nil
}

// PriceInventory is what one resolution of the price registry saw: the
// providers it can reach, and the accounts it could have used and did not.
type PriceInventory struct {
	Providers map[string]marketdata.PriceProvider
	Skipped   []Skipped
}

// PriceProvidersFor resolves the effective price provider registry for the
// user: credential-free sources overlaid by system-shared accounts and then the
// user's own (most specific wins per provider slug).
func (r *Resolver) PriceProvidersFor(ctx context.Context, userID string) (map[string]marketdata.PriceProvider, error) {
	inv, err := r.PriceInventoryFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	return inv.Providers, nil
}

// PriceInventoryFor resolves the same registry as PriceProvidersFor and reports
// what it left out along the way.
//
// The skipped list is the point. A startup line naming only the providers it
// reached is a complete, correct and reassuring sentence that says nothing about
// the configured account sitting unused beside them — which is exactly how prod
// went six days without refreshing a crypto price.
func (r *Resolver) PriceInventoryFor(ctx context.Context, userID string) (PriceInventory, error) {
	providers := make(map[string]marketdata.PriceProvider, len(r.cfg.KeylessPriceProviders))
	maps.Copy(providers, r.cfg.KeylessPriceProviders)

	candidates, skipped, err := r.accountsFor(ctx, userID, entity.CapabilityMarketData)
	if err != nil {
		return PriceInventory{}, err
	}

	// candidates are ordered user-first; iterate in reverse so system accounts
	// apply first and the user's own credentials overwrite them.
	for i := len(candidates) - 1; i >= 0; i-- {
		a := candidates[i]
		slug := a.Data[DataProviderKey]
		factory, ok := r.cfg.PriceProviders[slug]
		if !ok {
			// An account claiming market data whose provider this build cannot
			// talk to: a slug typo, or an adapter that left with an upgrade.
			// Either way its owner entered a credential that prices nothing.
			skipped = append(skipped, Skipped{
				Provider:  slug,
				AccountID: a.ID,
				Reason:    "no price adapter is registered for this provider",
			})
			continue
		}
		client, err := r.clientFor("price_provider", a, func() (any, error) { return factory(a) })
		if err != nil {
			// One account that cannot be built costs its own provider, not the
			// registry. Failing the whole resolution here would let a single
			// misconfigured credential stop every price in the sweep —
			// CoinGecko, MOEX, the FX leg — which is a far larger outage than
			// the thing that is actually broken.
			//
			// Loud rather than silent: an unusable account is a configuration
			// problem its owner has to see, and quiet degradation is what let
			// stale env credentials serve the sweep for days (personal-cpw).
			if r.cfg.Log != nil {
				r.cfg.Log.Warn("price provider account unusable, skipped",
					slog.String("provider", slug), slog.String("account_id", a.ID),
					slog.Any("error", err))
			}
			skipped = append(skipped, Skipped{
				Provider:  slug,
				AccountID: a.ID,
				Reason:    "account unusable: " + err.Error(),
			})
			continue
		}
		providers[slug] = client.(marketdata.PriceProvider)
	}

	return PriceInventory{Providers: providers, Skipped: skipped}, nil
}
