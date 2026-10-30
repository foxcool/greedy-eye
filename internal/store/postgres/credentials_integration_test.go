//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/provider"
	"github.com/foxcool/greedy-eye/internal/provider/catalog"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFreshDatabaseResolvesKeylessSources: a migrated database with no account
// in it — for the scheduler and for a user who has entered no credential — still
// prices from the public feeds and reads every keyless chain. Nobody has to
// create a row first.
func TestFreshDatabaseResolvesKeylessSources(t *testing.T) {
	ctx := context.Background()
	pool := getTestPool(t)
	user := createTestUser(t, NewUserStore(pool))

	providers := provider.New(ratelimit.NewRegistry(nil))
	resolver := credentials.NewResolver(credentials.Config{
		Source:                NewPortfolioStore(pool),
		WalletSyncers:         providers.WalletSyncers(),
		ExchangeSyncers:       providers.ExchangeSyncers(),
		BrokerSyncers:         providers.BrokerSyncers(),
		PriceProviders:        providers.PriceProviders(),
		KeylessWalletSyncers:  providers.KeylessWalletSyncers(),
		KeylessPriceProviders: providers.KeylessPriceProviders(),
	})

	// Checked against the catalogue, which is what tells a person "no account
	// needed", rather than against the registry that is supposed to honour it.
	for _, userID := range []string{"", user.ID} {
		prices, err := resolver.PriceProvidersFor(ctx, userID)
		require.NoError(t, err)

		for _, d := range providers.Descriptors() {
			if !d.Keyless {
				continue
			}
			for _, k := range d.Kinds {
				switch k {
				case catalog.KindPrice:
					assert.Contains(t, prices, d.Slug, "user %q: keyless price feed %q unresolved", userID, d.Slug)
				case catalog.KindWallet:
					for _, chain := range d.Chains {
						syncer, err := resolver.WalletSyncerFor(ctx, userID, "", []string{chain})
						require.NoError(t, err)
						assert.NotNil(t, syncer, "user %q: chain %q has no reader, keyless %q should serve it", userID, chain, d.Slug)
					}
				}
			}
		}
	}

	inv, err := resolver.Inventory(ctx)
	require.NoError(t, err)
	origins := map[string]string{}
	for _, p := range inv.Providers {
		origins[p.Provider] = p.String()
	}
	assert.Equal(t, "price=keyless", origins["cbr"])
	assert.Equal(t, "wallet=keyless", origins["blockchair"])
	assert.Empty(t, inv.Skipped)
}
