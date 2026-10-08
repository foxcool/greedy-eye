package credentials

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSource struct {
	user   map[string][]*entity.Account // userID → accounts
	system []*entity.Account
	err    error
}

func (f *fakeSource) ListUserAccountsByCapability(_ context.Context, userID string, _ entity.AccountCapability) ([]*entity.Account, error) {
	return f.user[userID], f.err
}

func (f *fakeSource) ListSystemAccountsByCapability(_ context.Context, _ entity.AccountCapability) ([]*entity.Account, error) {
	return f.system, f.err
}

// ListCapabilityOwners answers from the same map the accounts come from, so a
// fixture cannot describe an owner who holds nothing.
func (f *fakeSource) ListCapabilityOwners(_ context.Context, _ entity.AccountCapability) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	owners := make([]string, 0, len(f.user))
	for userID, accounts := range f.user {
		if len(accounts) > 0 {
			owners = append(owners, userID)
		}
	}
	sort.Strings(owners)
	return owners, nil
}

type fakeSyncer struct{ name string }

func (f *fakeSyncer) SyncWallet(_ context.Context, _ string, _ []string) ([]entity.WalletBalance, error) {
	return nil, nil
}

type fakeProvider struct{ name string }

func (f *fakeProvider) FetchPrices(_ context.Context, _ []*entity.Asset) ([]entity.StoredPrice, error) {
	return nil, nil
}
func (f *fakeProvider) BaseAssetSymbol() string         { return "USD" }
func (f *fakeProvider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

func account(id, provider string, updatedAt time.Time) *entity.Account {
	return &entity.Account{
		ID:        id,
		Data:      map[string]string{DataProviderKey: provider, "api_key": "k-" + id},
		UpdatedAt: updatedAt,
	}
}

func syncerFactory(builds *int) WalletSyncerFactory {
	return func(a *entity.Account) (entity.WalletSyncer, error) {
		*builds++
		return &fakeSyncer{name: a.ID}, nil
	}
}

func TestWalletSyncerForPrefersUserAccount(t *testing.T) {
	now := time.Now()
	var builds int
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {account("own", "moralis", now)}},
			system: []*entity.Account{account("shared", "moralis", now)},
		},
		WalletSyncers: map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	assert.Equal(t, "own", s.(*fakeSyncer).name)
}

func TestWalletSyncerForFallsBackToSystem(t *testing.T) {
	now := time.Now()
	var builds int
	r := NewResolver(Config{
		Source:        &fakeSource{system: []*entity.Account{account("shared", "moralis", now)}},
		WalletSyncers: map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	assert.Equal(t, "shared", s.(*fakeSyncer).name)

	// No candidates at all resolves to nothing. There is no catch-all to fall
	// back on: one would answer for chains it cannot reach.
	r = NewResolver(Config{
		Source:        &fakeSource{},
		WalletSyncers: map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
	})
	s, err = r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	assert.Nil(t, s)
}

func TestWalletSyncerForSkipsUnknownProviderSlug(t *testing.T) {
	now := time.Now()
	var builds int
	r := NewResolver(Config{
		Source: &fakeSource{
			user: map[string][]*entity.Account{"u1": {
				account("etherscan-key", "etherscan", now), // no factory registered
			}},
		},
		WalletSyncers: map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	assert.Nil(t, s)
	assert.Zero(t, builds)
}

// TestWalletSyncerForRoutesByChain is the core of non-EVM support: each
// provider serves its own ecosystem, and an account must reach exactly the
// syncer that covers its chains — never a foreign one, which would report an
// empty wallet instead of failing.
func TestWalletSyncerForRoutesByChain(t *testing.T) {
	now := time.Now()
	var evmBuilds, dotBuilds int
	source := &fakeSource{user: map[string][]*entity.Account{"u1": {
		account("moralis-key", "moralis", now),
		account("subscan-key", "subscan", now),
	}}}
	cfg := Config{
		Source: source,
		WalletSyncers: map[string]WalletProvider{
			"moralis": {Factory: syncerFactory(&evmBuilds), Chains: []string{"eth", "base", "polygon"}},
			"subscan": {Factory: syncerFactory(&dotBuilds), Chains: []string{"polkadot", "kusama"}},
		},
	}

	tests := []struct {
		name   string
		chains []string
		want   string // resolved account ID, "" when nothing must match
	}{
		{"single evm chain", []string{"eth"}, "moralis-key"},
		{"several evm chains", []string{"eth", "polygon"}, "moralis-key"},
		{"substrate chain", []string{"polkadot"}, "subscan-key"},
		{"substrate multi-network", []string{"polkadot", "kusama"}, "subscan-key"},
		{"unknown chain resolves nothing", []string{"ton"}, ""},
		// A provider must cover every requested chain: partial coverage would
		// silently drop the balances of the chain it cannot reach.
		{"chains split across providers", []string{"eth", "polkadot"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", "0xabc", tt.chains)
			require.NoError(t, err)
			if tt.want == "" {
				assert.Nil(t, s)
				return
			}
			require.NotNil(t, s)
			assert.Equal(t, tt.want, s.(*fakeSyncer).name)
		})
	}
}

// TestWalletSyncerForAutoDiscoveryRoutesByAddress covers accounts that name no
// chain — the common case, since the UI does not require one. There is nothing
// but the address to route on, so each provider claims its own address shape
// and then sweeps its own chains.
//
// This is also the regression guard for EVM accounts created before chain
// routing existed: they carry an address and nothing else.
func TestWalletSyncerForAutoDiscoveryRoutesByAddress(t *testing.T) {
	now := time.Now()
	var evmBuilds, dotBuilds int
	cfg := Config{
		Source: &fakeSource{user: map[string][]*entity.Account{"u1": {
			account("moralis-key", "moralis", now),
			account("subscan-key", "subscan", now),
		}}},
		WalletSyncers: map[string]WalletProvider{
			"moralis": {
				Factory:        syncerFactory(&evmBuilds),
				Chains:         []string{"eth", "base"},
				HandlesAddress: func(a string) bool { return strings.HasPrefix(a, "0x") },
			},
			"subscan": {
				Factory:        syncerFactory(&dotBuilds),
				Chains:         []string{"polkadot", "kusama"},
				HandlesAddress: func(a string) bool { return !strings.HasPrefix(a, "0x") },
			},
		},
	}

	tests := []struct {
		name    string
		address string
		want    string
	}{
		{"evm address", "0x75304308839f839a553b60b5671bb2f043420167", "moralis-key"},
		{"ss58 address", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", "subscan-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", tt.address, nil)
			require.NoError(t, err)
			require.NotNil(t, s)
			assert.Equal(t, tt.want, s.(*fakeSyncer).name)
		})
	}
}

// TestWalletSyncerForAutoDiscoveryNeedsAnAddressClaim: a provider that does not
// claim address shapes stays out of auto-discovery rather than being tried
// blindly, which would sync the wrong ecosystem and report an empty wallet.
func TestWalletSyncerForAutoDiscoveryNeedsAnAddressClaim(t *testing.T) {
	now := time.Now()
	var builds int
	cfg := Config{
		Source:        &fakeSource{user: map[string][]*entity.Account{"u1": {account("subscan-key", "subscan", now)}}},
		WalletSyncers: map[string]WalletProvider{"subscan": {Factory: syncerFactory(&builds), Chains: []string{"polkadot"}}},
	}
	s, err := NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	assert.Nil(t, s)

	// A provider declaring no chains stays catch-all for any chain.
	cfg.WalletSyncers = map[string]WalletProvider{"subscan": {Factory: syncerFactory(&builds)}}
	s, err = NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", "0xabc", []string{"anything"})
	require.NoError(t, err)
	assert.Equal(t, "subscan-key", s.(*fakeSyncer).name)
}

func TestResolverPropagatesSourceErrors(t *testing.T) {
	r := NewResolver(Config{Source: &fakeSource{err: errors.New("db down")}})

	_, err := r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	assert.ErrorContains(t, err, "db down")
	_, err = r.PriceProvidersFor(context.Background(), "u1")
	assert.ErrorContains(t, err, "db down")
}

// PriceProviderFactory alias sanity: keep marketdata import used even if tests change.
var _ marketdata.PriceProvider = (*fakeProvider)(nil)
var _ entity.WalletSyncer = (*fakeSyncer)(nil)

// TestNoSyncerRatherThanTheWrongOne: with no account routing to this address
// there is no fallback. A catch-all would reach for an EVM syncer on a
// Substrate address and report an empty wallet — which reads as "you hold
// nothing" rather than as an error, and is worse than having no syncer at all.
func TestNoSyncerRatherThanTheWrongOne(t *testing.T) {
	r := NewResolver(Config{Source: &fakeSource{}})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	assert.Nil(t, s)
}

// TestAccountOverridesAKeylessDefault: a public feed is registered by default,
// and an account naming the same slug replaces it. That is how a free source
// gets throttled after an enforcement notice, or given one deployment a smaller
// share — without the service growing a setting for it.
func TestAccountOverridesAKeylessDefault(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{system: []*entity.Account{account("sys-cbr", "cbr", now)}},
		PriceProviders: map[string]PriceProviderFactory{
			"cbr": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
		KeylessPriceProviders: map[string]marketdata.PriceProvider{
			"cbr": &fakeProvider{name: "default-cbr"},
		},
	})

	got, err := r.PriceProvidersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, "sys-cbr", got["cbr"].(*fakeProvider).name)
}

// TestUnattendedWorkUsesTheSoleOperator: a sweep runs with nobody in context, so
// it sees only accounts carrying system_scopes — and setting those needs the
// admin role, which no RPC grants. On a self-hosted instance that is a deadlock:
// the operator enters a key, the scheduler cannot see it, and prod 2026-07-25
// spent days quietly using stale environment credentials instead.
func TestUnattendedWorkUsesTheSoleOperator(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{
			user: map[string][]*entity.Account{"u1": {account("cg", "coingecko", now)}},
			// No system accounts: nobody granted a scope, because nobody can.
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	providers, err := r.PriceProvidersFor(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, providers, "coingecko")
	assert.Equal(t, "cg", providers["coingecko"].(*fakeProvider).name)
}

// TestUnattendedWorkDeclinesBetweenOperators: with more than one credential
// holder the distinction stops being ceremony — one person's plan must not be
// spent on everyone's behalf — so the fallback refuses, and says so instead of
// leaving an operator to wonder why the sweep found nothing.
func TestUnattendedWorkDeclinesBetweenOperators(t *testing.T) {
	now := time.Now()
	var logs bytes.Buffer
	r := NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"u1": {account("cg-1", "coingecko", now)},
			"u2": {account("cg-2", "coingecko", now)},
		}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
		Log: slog.New(slog.NewTextHandler(&logs, nil)),
	})

	providers, err := r.PriceProvidersFor(context.Background(), "")
	require.NoError(t, err)
	assert.NotContains(t, providers, "coingecko", "neither operator's plan is spent by default")
	assert.Contains(t, logs.String(), "will not choose between operators")
}

// TestSoleOperatorFallbackIsPerProvider: prod 2026-08-17. Three accounts carried
// a market-data system scope, so the candidate list was not empty and the
// capability-wide fallback never fired — while binance, the one live crypto
// source, carried no scope and was simply absent. The sweep refreshed no crypto
// price for six days, and every manual fetch kept working, because those carry a
// user id.
func TestSoleOperatorFallbackIsPerProvider(t *testing.T) {
	now := time.Now()
	priced := func(a *entity.Account) (marketdata.PriceProvider, error) {
		return &fakeProvider{name: a.ID}, nil
	}
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {account("binance-own", "binance", now)}},
			system: []*entity.Account{account("cg-sys", "coingecko", now)},
		},
		PriceProviders: map[string]PriceProviderFactory{"coingecko": priced, "binance": priced},
	})

	providers, err := r.PriceProvidersFor(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "cg-sys", providers["coingecko"].(*fakeProvider).name, "a granted scope still wins its own slug")
	require.Contains(t, providers, "binance", "a scope on one provider must not hide another")
	assert.Equal(t, "binance-own", providers["binance"].(*fakeProvider).name)
}

// TestSoleOperatorDoesNotOverrideASystemAccount: an explicitly scoped account
// stays the supported path. The fallback exists for instances that have no way
// to grant a scope, not to overrule one that was granted.
func TestSoleOperatorDoesNotOverrideASystemAccount(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {account("personal", "coingecko", now)}},
			system: []*entity.Account{account("shared", "coingecko", now)},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	providers, err := r.PriceProvidersFor(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "shared", providers["coingecko"].(*fakeProvider).name)
}

// TestInventoryNamesWhatItPassedOver: the startup line is the only surface where
// an unattended-only failure can appear, and a list of what worked cannot show
// an absence. Both skips here price nothing and neither raises an error.
func TestInventoryNamesWhatItPassedOver(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"u1": {account("typo", "coingeko", now)},
		}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, inv.Providers)
	require.Len(t, inv.Skipped, 1)
	assert.Equal(t, "coingeko", inv.Skipped[0].Provider)
	assert.Equal(t, "typo", inv.Skipped[0].AccountID)
	assert.Contains(t, inv.Skipped[0].Reason, "no price adapter")

	// Several credential holders: nobody's plan is spent by default, and the
	// reason says so rather than leaving the registry quietly short.
	r = NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"u1": {account("cg-1", "coingecko", now)},
			"u2": {account("cg-2", "coingecko", now)},
		}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err = r.PriceInventoryFor(context.Background(), "")
	require.NoError(t, err)
	assert.NotContains(t, inv.Providers, "coingecko")
	require.Len(t, inv.Skipped, 1)
	assert.Contains(t, inv.Skipped[0].Reason, "2 operators hold")
}

// TestUnusableAccountIsReportedNotFatal: one credential that cannot be built
// costs its own provider and is named, not the whole registry — a single
// misconfigured key must not stop CoinGecko, MOEX and the FX leg at once.
func TestUnusableAccountIsReportedNotFatal(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{system: []*entity.Account{
			account("broken", "coingecko", now),
			account("fine", "moex", now),
		}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(*entity.Account) (marketdata.PriceProvider, error) {
				return nil, errors.New("api key rejected")
			},
			"moex": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "")
	require.NoError(t, err)
	assert.Contains(t, inv.Providers, "moex")
	assert.NotContains(t, inv.Providers, "coingecko")
	require.Len(t, inv.Skipped, 1)
	assert.Equal(t, "broken", inv.Skipped[0].AccountID)
	assert.Contains(t, inv.Skipped[0].Reason, "api key rejected")
}

// TestUserRequestIsUnaffectedBySoleOperator: the fallback is for work with
// nobody in context. A request made BY someone resolves their own accounts and
// must not start borrowing another operator's key.
func TestUserRequestIsUnaffectedBySoleOperator(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"owner": {account("theirs", "coingecko", now)},
		}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	providers, err := r.PriceProvidersFor(context.Background(), "someone-else")
	require.NoError(t, err)
	assert.NotContains(t, providers, "coingecko")
}

// TestKeylessSyncerNeedsNoAccount: a wallet syncer is chosen from accounts
// carrying onchain_lookup, and a wallet account holds only an address — it never
// names the reader. So before keyless readers were registered by default, a
// fresh instance synced NOTHING until somebody hand-created a service row per
// ecosystem, and nothing said so.
func TestKeylessSyncerNeedsNoAccount(t *testing.T) {
	r := NewResolver(Config{
		Source: &fakeSource{},
		KeylessWalletSyncers: map[string]WalletProvider{
			"esplora": {
				Factory:        func(*entity.Account) (entity.WalletSyncer, error) { return &fakeSyncer{name: "esplora"}, nil },
				Chains:         []string{"bitcoin"},
				HandlesAddress: func(a string) bool { return strings.HasPrefix(a, "bc1") },
			},
		},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "bc1qexample", nil)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, "esplora", s.(*fakeSyncer).name)
}

// TestKeylessSyncerStillRoutes: needing no credential does not make a reader a
// catch-all. Serving a Substrate address from a Bitcoin explorer would report an
// empty wallet, which reads as "you hold nothing" rather than as an error.
func TestKeylessSyncerStillRoutes(t *testing.T) {
	r := NewResolver(Config{
		Source: &fakeSource{},
		KeylessWalletSyncers: map[string]WalletProvider{
			"esplora": {
				Factory:        func(*entity.Account) (entity.WalletSyncer, error) { return &fakeSyncer{name: "esplora"}, nil },
				Chains:         []string{"bitcoin"},
				HandlesAddress: func(a string) bool { return strings.HasPrefix(a, "bc1") },
			},
		},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "5Dsvsa", []string{"polkadot"})
	require.NoError(t, err)
	assert.Nil(t, s)
}

// TestAccountBeatsAKeylessSyncer: an account naming the same slug wins, which is
// how a free reader gets throttled or pointed at a different endpoint.
func TestAccountBeatsAKeylessSyncer(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{
			system: []*entity.Account{account("acct-tonapi", "tonapi", now)},
		},
		WalletSyncers: map[string]WalletProvider{
			"tonapi": {
				Factory:        func(a *entity.Account) (entity.WalletSyncer, error) { return &fakeSyncer{name: a.ID}, nil },
				HandlesAddress: func(string) bool { return true },
			},
		},
		KeylessWalletSyncers: map[string]WalletProvider{
			"tonapi": {
				Factory:        func(*entity.Account) (entity.WalletSyncer, error) { return &fakeSyncer{name: "keyless"}, nil },
				HandlesAddress: func(string) bool { return true },
			},
		},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", "UQAny", nil)
	require.NoError(t, err)
	assert.Equal(t, "acct-tonapi", s.(*fakeSyncer).name)
}

// pausedProvider is a price provider whose credential cannot carry unattended
// work until a known time.
type pausedProvider struct {
	fakeProvider
	until time.Time
}

func (p *pausedProvider) UnusableUntil() (string, time.Time, bool) {
	return "paused after repeated refusals", p.until, true
}

// TestShadowedDuplicateIsNamed: of two accounts of one level for one
// provider, the first in resolution order is used and the other is never
// asked. That is a working credential producing nothing, and the inventory
// says so. A user's own account overriding a shared one is not that — the
// shared one serves everybody else — and the same account listed twice (owned
// and system scoped) shadows nothing.
func TestShadowedDuplicateIsNamed(t *testing.T) {
	now := time.Now()
	owned := func(id string) *entity.Account {
		a := account(id, "coingecko", now)
		a.UserID = "u1"
		return a
	}
	older, newer := owned("older"), owned("newer")
	foreign := account("shared", "coingecko", now)
	foreign.UserID = "admin"
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {older, newer}},
			system: []*entity.Account{foreign},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, "older", inv.Providers["coingecko"].(*fakeProvider).name)
	assert.Equal(t, "older", inv.Serving["coingecko"])

	shadowed := map[string]bool{}
	for _, s := range inv.Skipped {
		assert.Equal(t, entity.SkipShadowed, s.Kind)
		shadowed[s.AccountID] = true
	}
	assert.Equal(t, map[string]bool{"newer": true}, shadowed)

	// Two shared accounts for one provider: the second is dead for everyone.
	other := account("shared-2", "coingecko", now)
	other.UserID = "admin"
	r = NewResolver(Config{
		Source: &fakeSource{system: []*entity.Account{foreign, other}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})
	inv, err = r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, inv.Skipped, 1)
	assert.Equal(t, "shared-2", inv.Skipped[0].AccountID)
	assert.Equal(t, entity.SkipShadowed, inv.Skipped[0].Kind)
}

// TestPriceSourceHealthReportsThePauseAsValues: the deadline arrives as a
// time, not inside a log phrase, and a credential-free source names no account.
func TestPriceSourceHealthReportsThePauseAsValues(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	r := NewResolver(Config{
		Source: &fakeSource{system: []*entity.Account{account("cg", "coingecko", now)}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &pausedProvider{fakeProvider: fakeProvider{name: a.ID}, until: until}, nil
			},
		},
		KeylessPriceProviders: map[string]marketdata.PriceProvider{"moex": &fakeProvider{name: "moex"}},
	})

	report, err := r.PriceSourceHealth(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, report.Sources, 2)

	cg, moex := report.Sources[0], report.Sources[1]
	assert.Equal(t, "coingecko", cg.Provider)
	assert.Equal(t, "cg", cg.AccountID)
	assert.True(t, cg.Unusable)
	assert.Equal(t, until, cg.Until)

	assert.Equal(t, "moex", moex.Provider)
	assert.Empty(t, moex.AccountID)
	assert.False(t, moex.Unusable)
}

// TestInventoryCountsEachAccountOnceAndCopiesOfAKeyAsOne: an account both owned and system scoped is one
// account, so it is passed over once; and accounts sharing one key — every
// account a broker token reaches — are one credential, not duplicates.
func TestInventoryCountsEachAccountOnceAndCopiesOfAKeyAsOne(t *testing.T) {
	now := time.Now()
	scoped := account("gate", "gateio", now)
	scoped.SystemScopes = []entity.AccountCapability{entity.CapabilityMarketData}
	copyA := account("tinvest-1", "tinvest", now)
	copyB := account("tinvest-2", "tinvest", now)
	copyB.Data["api_key"] = copyA.Data["api_key"]

	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {scoped, copyA, copyB}},
			system: []*entity.Account{scoped},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"tinvest": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, inv.Skipped, 1)
	assert.Equal(t, "gate", inv.Skipped[0].AccountID)
	assert.Equal(t, entity.SkipNoAdapter, inv.Skipped[0].Kind)
}

// TestOwnedAndSharedAccountKeepsItsOwnPriority: an account the user owns that
// is also shared arrives twice. Counting it once must not demote it to its
// shared position, where a later own account would overwrite it and change the
// client that prices for this user.
func TestOwnedAndSharedAccountKeepsItsOwnPriority(t *testing.T) {
	now := time.Now()
	first := account("first", "coingecko", now)
	first.UserID = "u1"
	first.SystemScopes = []entity.AccountCapability{entity.CapabilityMarketData}
	second := account("second", "coingecko", now)
	second.UserID = "u1"
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {first, second}},
			system: []*entity.Account{first},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, "first", inv.Providers["coingecko"].(*fakeProvider).name)
	require.Len(t, inv.Skipped, 1)
	assert.Equal(t, "second", inv.Skipped[0].AccountID)
	assert.Equal(t, entity.SkipShadowed, inv.Skipped[0].Kind)
}

// TestOwnKeyOutranksAnOlderSharedOne: the user's own shared account must keep
// beating another owner's shared account of the same provider, and that is the
// intended override, not shadowing.
func TestOwnKeyOutranksAnOlderSharedOne(t *testing.T) {
	now := time.Now()
	scopes := []entity.AccountCapability{entity.CapabilityMarketData}
	theirs := account("a-theirs", "coingecko", now)
	theirs.UserID, theirs.SystemScopes = "admin", scopes
	mine := account("b-mine", "coingecko", now)
	mine.UserID, mine.SystemScopes = "u1", scopes
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {mine}},
			system: []*entity.Account{theirs, mine},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, "b-mine", inv.Providers["coingecko"].(*fakeProvider).name)
	assert.Empty(t, inv.Skipped)
}

func disabledAccount(a *entity.Account) *entity.Account {
	at := time.Now().Add(-time.Hour)
	a.DisabledAt = &at
	return a
}

// TestDisabledDuplicateHandsOverToTheOther: the 02.09 shape. A lapsed key
// was the older of two accounts for one provider, so it kept being chosen;
// disabling it must let the other serve, and the other is then not shadowed —
// it is the one in use.
func TestDisabledDuplicateHandsOverToTheOther(t *testing.T) {
	now := time.Now()
	owned := func(id string) *entity.Account {
		a := account(id, "coingecko", now)
		a.UserID = "u1"
		return a
	}
	r := NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"u1": {disabledAccount(owned("older")), owned("newer")},
		}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, "newer", inv.Serving["coingecko"])
	require.Len(t, inv.Skipped, 1, "no SHADOWED once the older is out of the running")
	assert.Equal(t, entity.SkipDisabled, inv.Skipped[0].Kind)
	assert.Equal(t, "older", inv.Skipped[0].AccountID)
	assert.Equal(t, "coingecko", inv.Skipped[0].Provider)
}

// TestDisabledSystemAccountDoesNotClaimItsSlug: a disabled shared account
// filtered only at the end would still cover its provider in the sole-operator
// fallback, and the operator's working key for it would never be asked.
func TestDisabledSystemAccountDoesNotClaimItsSlug(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {account("own", "coingecko", now)}},
			system: []*entity.Account{disabledAccount(account("shared", "coingecko", now))},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	providers, err := r.PriceProvidersFor(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, providers, "coingecko")
	assert.Equal(t, "own", providers["coingecko"].(*fakeProvider).name)
}

// TestDisabledAccountIsNotASyncer: chain lookups go through the same funnel as
// prices, so a disabled onchain account is never the one a wallet syncs with.
func TestDisabledAccountIsNotASyncer(t *testing.T) {
	now := time.Now()
	builds := 0
	r := NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"u1": {disabledAccount(account("lapsed", "moralis", now)), account("live", "alchemy", now)},
		}},
		WalletSyncers: map[string]WalletProvider{
			"moralis": {Factory: syncerFactory(&builds)},
			"alchemy": {Factory: syncerFactory(&builds)},
		},
	})

	syncer, err := r.WalletSyncerFor(context.Background(), "u1", "0xabc", nil)
	require.NoError(t, err)
	require.NotNil(t, syncer)
	assert.Equal(t, "live", syncer.(*fakeSyncer).name)
	assert.Equal(t, 1, builds, "the disabled account's client is never built")
}

// TestDisabledAccountWithoutAPriceAdapterIsNoSource: disabling an account
// whose slug never priced anything must not invent a disabled price source.
func TestDisabledAccountWithoutAPriceAdapterIsNoSource(t *testing.T) {
	now := time.Now()
	r := NewResolver(Config{
		Source: &fakeSource{user: map[string][]*entity.Account{
			"u1": {disabledAccount(account("scan", "subscan", now))},
		}},
		PriceProviders: map[string]PriceProviderFactory{},
	})

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Empty(t, inv.Skipped)
}
