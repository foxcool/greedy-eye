// Package credentials resolves provider clients from account credentials
// stored in the database, falling back to env-configured clients. Resolution
// order per the account-capabilities design: the user's own account with the
// capability → an admin-shared system account → env fallback.
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
}

// WalletSyncerFactory builds a wallet syncer from account credentials.
type WalletSyncerFactory func(a *entity.Account) (entity.WalletSyncer, error)

// WalletProvider couples a syncer factory with the chains that provider covers.
// Routing by chain is what lets EVM (Moralis) and non-EVM ecosystems coexist:
// an account naming chain "polkadot" must never reach an EVM-only syncer.
type WalletProvider struct {
	Factory WalletSyncerFactory

	// Chains this provider can sync. An empty list marks a catch-all provider
	// usable for any chain — including auto-discovery, where the account names
	// no chain at all.
	Chains []string
}

// covers reports whether the provider can sync every requested chain. No
// requested chains means auto-discovery, which only a catch-all can serve:
// a chain-scoped provider has no way to enumerate an address's activity.
func (p WalletProvider) covers(chains []string) bool {
	if len(p.Chains) == 0 {
		return true
	}
	if len(chains) == 0 {
		return false
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

// PriceProviderFactory builds a price provider from account credentials.
type PriceProviderFactory func(a *entity.Account) (marketdata.PriceProvider, error)

// Config wires a Resolver: per-provider factories plus env-configured fallbacks.
type Config struct {
	Source          AccountSource
	WalletSyncers   map[string]WalletProvider        // keyed by provider slug
	ExchangeSyncers map[string]ExchangeSyncerFactory // keyed by provider slug
	PriceProviders  map[string]PriceProviderFactory  // keyed by provider slug

	// EnvWalletSyncer is the deprecated env-configured fallback (g27). Its
	// chains are declared separately because, unlike account-based providers,
	// it carries no provider slug to look them up by; empty means catch-all.
	EnvWalletSyncer       entity.WalletSyncer // may be nil
	EnvWalletSyncerChains []string

	EnvPriceProviders map[string]marketdata.PriceProvider // may be empty
	Log               *slog.Logger
}

// Resolver resolves per-account provider clients with an in-memory cache
// invalidated by the account's updated_at timestamp.
type Resolver struct {
	cfg Config

	mu    sync.Mutex
	cache map[string]cacheEntry // "<kind>:<account_id>" → built client

	warnMu     sync.Mutex
	warnedEnvs map[string]bool // env-fallback warnings already emitted, keyed by kind/slug
}

type cacheEntry struct {
	updatedAt time.Time
	client    any
}

func NewResolver(cfg Config) *Resolver {
	return &Resolver{cfg: cfg, cache: map[string]cacheEntry{}, warnedEnvs: map[string]bool{}}
}

// warnEnvFallback logs once per process per key that an env-configured client
// was used instead of account-based credentials. Env keys are deprecated
// (g27): migrate them into system accounts.
func (r *Resolver) warnEnvFallback(key string) {
	r.warnMu.Lock()
	defer r.warnMu.Unlock()
	if r.warnedEnvs[key] {
		return
	}
	r.warnedEnvs[key] = true
	if r.cfg.Log != nil {
		r.cfg.Log.Warn("env provider credentials used (deprecated); migrate to a system account", slog.String("provider", key))
	}
}

// accountsFor returns candidate accounts for the capability in resolution
// order: the user's own accounts first, then system-shared ones.
func (r *Resolver) accountsFor(ctx context.Context, userID string, capability entity.AccountCapability) ([]*entity.Account, error) {
	var candidates []*entity.Account
	if userID != "" {
		own, err := r.cfg.Source.ListUserAccountsByCapability(ctx, userID, capability)
		if err != nil {
			return nil, fmt.Errorf("list user accounts: %w", err)
		}
		candidates = append(candidates, own...)
	}
	system, err := r.cfg.Source.ListSystemAccountsByCapability(ctx, capability)
	if err != nil {
		return nil, fmt.Errorf("list system accounts: %w", err)
	}
	return append(candidates, system...), nil
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
// auto-discovery and only matches a catch-all provider. Returns the
// env-configured syncer (possibly nil) when no account-based credentials match.
func (r *Resolver) WalletSyncerFor(ctx context.Context, userID string, chains []string) (entity.WalletSyncer, error) {
	candidates, err := r.accountsFor(ctx, userID, entity.CapabilityOnchainLookup)
	if err != nil {
		return nil, err
	}

	for _, a := range candidates {
		provider, ok := r.cfg.WalletSyncers[a.Data[DataProviderKey]]
		if !ok || !provider.covers(chains) {
			continue
		}
		client, err := r.clientFor("wallet_syncer", a, func() (any, error) { return provider.Factory(a) })
		if err != nil {
			return nil, fmt.Errorf("build wallet syncer from account %s: %w", a.ID, err)
		}
		return client.(entity.WalletSyncer), nil
	}

	if r.cfg.EnvWalletSyncer == nil {
		return nil, nil
	}
	// The env syncer is subject to the same chain routing: falling back to an
	// EVM-only syncer for a Substrate account would report an empty wallet
	// rather than an error, which is worse than having no syncer at all.
	if !(WalletProvider{Chains: r.cfg.EnvWalletSyncerChains}).covers(chains) {
		return nil, nil
	}
	r.warnEnvFallback("wallet_syncer")
	return r.cfg.EnvWalletSyncer, nil
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

// PriceProvidersFor resolves the effective price provider registry for the
// user: env-configured providers overlaid by system-shared and then the
// user's own account credentials (most specific wins per provider slug).
func (r *Resolver) PriceProvidersFor(ctx context.Context, userID string) (map[string]marketdata.PriceProvider, error) {
	providers := make(map[string]marketdata.PriceProvider, len(r.cfg.EnvPriceProviders))
	maps.Copy(providers, r.cfg.EnvPriceProviders)

	candidates, err := r.accountsFor(ctx, userID, entity.CapabilityMarketData)
	if err != nil {
		return nil, err
	}

	// candidates are ordered user-first; iterate in reverse so system accounts
	// apply first and the user's own credentials overwrite them.
	accountBacked := make(map[string]bool, len(candidates))
	for i := len(candidates) - 1; i >= 0; i-- {
		a := candidates[i]
		slug := a.Data[DataProviderKey]
		factory, ok := r.cfg.PriceProviders[slug]
		if !ok {
			continue
		}
		client, err := r.clientFor("price_provider", a, func() (any, error) { return factory(a) })
		if err != nil {
			return nil, fmt.Errorf("build price provider from account %s: %w", a.ID, err)
		}
		providers[slug] = client.(marketdata.PriceProvider)
		accountBacked[slug] = true
	}

	for slug := range r.cfg.EnvPriceProviders {
		if !accountBacked[slug] {
			r.warnEnvFallback(slug)
		}
	}

	return providers, nil
}
