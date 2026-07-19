package credentials

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
		WalletSyncers:   map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
		EnvWalletSyncer: &fakeSyncer{name: "env"},
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Equal(t, "own", s.(*fakeSyncer).name)
}

func TestWalletSyncerForFallsBackToSystemThenEnv(t *testing.T) {
	now := time.Now()
	var builds int
	env := &fakeSyncer{name: "env"}
	r := NewResolver(Config{
		Source:          &fakeSource{system: []*entity.Account{account("shared", "moralis", now)}},
		WalletSyncers:   map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
		EnvWalletSyncer: env,
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Equal(t, "shared", s.(*fakeSyncer).name)

	// No candidates at all → env fallback.
	r = NewResolver(Config{
		Source:          &fakeSource{},
		WalletSyncers:   map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
		EnvWalletSyncer: env,
	})
	s, err = r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Same(t, env, s)
}

func TestWalletSyncerForSkipsUnknownProviderSlug(t *testing.T) {
	now := time.Now()
	var builds int
	env := &fakeSyncer{name: "env"}
	r := NewResolver(Config{
		Source: &fakeSource{
			user: map[string][]*entity.Account{"u1": {
				account("etherscan-key", "etherscan", now), // no factory registered
			}},
		},
		WalletSyncers:   map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
		EnvWalletSyncer: env,
	})

	s, err := r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Same(t, env, s)
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
			s, err := NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", tt.chains)
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

// TestWalletSyncerForCatchAllAndAutoDiscovery pins the two edges of chain
// routing: a provider declaring no chains serves anything, and auto-discovery
// (no chains requested) is the one case a chain-scoped provider cannot serve —
// it has no way to enumerate an address's activity.
func TestWalletSyncerForCatchAllAndAutoDiscovery(t *testing.T) {
	now := time.Now()
	var builds int

	scoped := Config{
		Source:        &fakeSource{user: map[string][]*entity.Account{"u1": {account("subscan-key", "subscan", now)}}},
		WalletSyncers: map[string]WalletProvider{"subscan": {Factory: syncerFactory(&builds), Chains: []string{"polkadot"}}},
	}
	s, err := NewResolver(scoped).WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Nil(t, s, "chain-scoped provider must not serve auto-discovery")

	catchAll := scoped
	catchAll.WalletSyncers = map[string]WalletProvider{"subscan": {Factory: syncerFactory(&builds)}}
	s, err = NewResolver(catchAll).WalletSyncerFor(context.Background(), "u1", []string{"anything"})
	require.NoError(t, err)
	assert.Equal(t, "subscan-key", s.(*fakeSyncer).name, "provider without declared chains serves any chain")
}

// TestWalletSyncerForEnvFallbackRespectsChains guards the deprecated env path
// (g27): it is Moralis, so a Substrate account must not land on it.
func TestWalletSyncerForEnvFallbackRespectsChains(t *testing.T) {
	env := &fakeSyncer{name: "env"}
	cfg := Config{
		Source:                &fakeSource{},
		EnvWalletSyncer:       env,
		EnvWalletSyncerChains: []string{"eth", "base"},
	}

	s, err := NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", []string{"eth"})
	require.NoError(t, err)
	assert.Same(t, env, s)

	s, err = NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", []string{"polkadot"})
	require.NoError(t, err)
	assert.Nil(t, s, "EVM-only env syncer must not serve a Substrate account")

	// An env syncer without declared chains keeps the pre-routing behaviour.
	cfg.EnvWalletSyncerChains = nil
	s, err = NewResolver(cfg).WalletSyncerFor(context.Background(), "u1", []string{"polkadot"})
	require.NoError(t, err)
	assert.Same(t, env, s)
}

func TestClientCacheInvalidatesOnUpdatedAt(t *testing.T) {
	now := time.Now()
	acc := account("own", "moralis", now)
	var builds int
	r := NewResolver(Config{
		Source:        &fakeSource{user: map[string][]*entity.Account{"u1": {acc}}},
		WalletSyncers: map[string]WalletProvider{"moralis": {Factory: syncerFactory(&builds)}},
	})

	_, err := r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	_, err = r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, builds, "second resolve must hit the cache")

	acc.UpdatedAt = now.Add(time.Minute) // credentials rotated
	_, err = r.WalletSyncerFor(context.Background(), "u1", nil)
	require.NoError(t, err)
	assert.Equal(t, 2, builds, "updated account must rebuild the client")
}

func TestPriceProvidersForOverlayOrder(t *testing.T) {
	now := time.Now()
	env := &fakeProvider{name: "env-binance"}
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {account("own-cg", "coingecko", now)}},
			system: []*entity.Account{account("sys-cg", "coingecko", now), account("sys-bin", "binance", now)},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
			"binance": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
		EnvPriceProviders: map[string]marketdata.PriceProvider{
			"binance":   env,
			"coingecko": &fakeProvider{name: "env-cg"},
		},
	})

	got, err := r.PriceProvidersFor(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "own-cg", got["coingecko"].(*fakeProvider).name, "user credentials win over system and env")
	assert.Equal(t, "sys-bin", got["binance"].(*fakeProvider).name, "system credentials win over env")
}

func TestResolverPropagatesSourceErrors(t *testing.T) {
	r := NewResolver(Config{Source: &fakeSource{err: errors.New("db down")}})

	_, err := r.WalletSyncerFor(context.Background(), "u1", nil)
	assert.ErrorContains(t, err, "db down")
	_, err = r.PriceProvidersFor(context.Background(), "u1")
	assert.ErrorContains(t, err, "db down")
}

// PriceProviderFactory alias sanity: keep marketdata import used even if tests change.
var _ marketdata.PriceProvider = (*fakeProvider)(nil)
var _ entity.WalletSyncer = (*fakeSyncer)(nil)

func TestEnvFallbackWarnsOncePerProvider(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	r := NewResolver(Config{
		Source:          &fakeSource{},
		EnvWalletSyncer: &fakeSyncer{name: "env"},
		EnvPriceProviders: map[string]marketdata.PriceProvider{
			"coingecko": &fakeProvider{name: "env-cg"},
		},
		Log: log,
	})

	for range 3 {
		_, err := r.WalletSyncerFor(context.Background(), "u1", nil)
		require.NoError(t, err)
		_, err = r.PriceProvidersFor(context.Background(), "u1")
		require.NoError(t, err)
	}

	out := buf.String()
	assert.Equal(t, 1, strings.Count(out, "provider=wallet_syncer"), out)
	assert.Equal(t, 1, strings.Count(out, "provider=coingecko"), out)
}

func TestNoEnvFallbackWarnWhenAccountBacked(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	now := time.Now()

	r := NewResolver(Config{
		Source: &fakeSource{system: []*entity.Account{account("sys", "coingecko", now)}},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
		EnvPriceProviders: map[string]marketdata.PriceProvider{
			"coingecko": &fakeProvider{name: "env-cg"},
		},
		Log: log,
	})

	_, err := r.PriceProvidersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "deprecated")
}
