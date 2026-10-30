package credentials

import (
	"context"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scoped(a *entity.Account, caps ...entity.AccountCapability) *entity.Account {
	a.SystemScopes = caps
	return a
}

func inventoryResolver(src AccountSource) *Resolver {
	price := func(a *entity.Account) (marketdata.PriceProvider, error) { return &fakeProvider{name: a.ID}, nil }
	wallet := WalletProvider{
		Factory: func(*entity.Account) (entity.WalletSyncer, error) { return &fakeSyncer{}, nil },
		Chains:  []string{"ton"},
	}
	return NewResolver(Config{
		Source:                src,
		PriceProviders:        map[string]PriceProviderFactory{"coingecko": price, "binance": price, "tinvest": price, "cbr": price},
		KeylessPriceProviders: map[string]marketdata.PriceProvider{"cbr": &fakeProvider{name: "cbr"}},
		WalletSyncers:         map[string]WalletProvider{"alchemy": wallet, "tonapi": wallet},
		KeylessWalletSyncers:  map[string]WalletProvider{"tonapi": wallet, "esplora": wallet},
		ExchangeSyncers:       map[string]ExchangeSyncerFactory{"binance": nil},
		BrokerSyncers:         map[string]BrokerSyncerFactory{"tinvest": nil},
	})
}

func roles(inv Inventory) map[string]string {
	out := map[string]string{}
	for _, p := range inv.Providers {
		out[p.Provider] = p.String()
	}
	return out
}

// TestInventoryNamesEveryProviderAndItsOrigin: the startup line has to show
// what did not resolve as plainly as what did, and say whether a source runs
// on a shared account, the operator's own, or no account at all.
func TestInventoryNamesEveryProviderAndItsOrigin(t *testing.T) {
	now := time.Now()
	src := &fakeSource{
		system: []*entity.Account{
			scoped(account("cg", "coingecko", now), entity.CapabilityMarketData),
			scoped(account("ton", "tonapi", now), entity.CapabilityOnchainLookup),
		},
		user: map[string][]*entity.Account{"op": {account("bn", "binance", now)}},
	}

	inv, err := inventoryResolver(src).Inventory(context.Background())
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"alchemy":   "wallet=unresolved",
		"binance":   "exchange=per_account price=operator",
		"cbr":       "price=keyless",
		"coingecko": "price=system",
		"esplora":   "wallet=keyless",
		"tinvest":   "broker=per_account price=unresolved",
		"tonapi":    "wallet=system",
	}, roles(inv))

	slugs := make([]string, len(inv.Providers))
	for i, p := range inv.Providers {
		slugs[i] = p.Provider
	}
	assert.IsIncreasing(t, slugs, "a startup line that reorders itself cannot be diffed between boots")
}

// TestFreshInstanceInventoryIsKeylessOnly: no accounts at all still leaves
// every keyless source resolved, and nothing claims an account.
func TestFreshInstanceInventoryIsKeylessOnly(t *testing.T) {
	inv, err := inventoryResolver(&fakeSource{}).Inventory(context.Background())
	require.NoError(t, err)

	got := roles(inv)
	assert.Equal(t, "price=keyless", got["cbr"])
	assert.Equal(t, "wallet=keyless", got["esplora"])
	assert.Equal(t, "wallet=keyless", got["tonapi"])
	assert.Equal(t, "price=unresolved", got["coingecko"])
	assert.Empty(t, inv.Skipped)
}

// TestDisabledAccountDoesNotResolveItsProvider: a stood-down account must not
// make its provider look served.
func TestDisabledAccountDoesNotResolveItsProvider(t *testing.T) {
	now := time.Now()
	off := scoped(account("cg", "coingecko", now), entity.CapabilityMarketData)
	off.DisabledAt = &now

	inv, err := inventoryResolver(&fakeSource{system: []*entity.Account{off}}).Inventory(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "price=unresolved", roles(inv)["coingecko"])
}

func TestInventoryPropagatesSourceErrors(t *testing.T) {
	_, err := inventoryResolver(&fakeSource{err: assert.AnError}).Inventory(context.Background())
	require.ErrorIs(t, err, assert.AnError)
}
