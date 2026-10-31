package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/provider"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emptySource is a database nobody has created an account in yet.
type emptySource struct{}

func (emptySource) ListUserAccountsByCapability(context.Context, string, entity.AccountCapability) ([]*entity.Account, error) {
	return nil, nil
}

func (emptySource) ListSystemAccountsByCapability(context.Context, entity.AccountCapability) ([]*entity.Account, error) {
	return nil, nil
}

func (emptySource) ListCapabilityOwners(context.Context, entity.AccountCapability) ([]string, error) {
	return nil, nil
}

// TestStartupInventoryIsOneLineNamingEveryProvider: the line is wired the way
// main wires it, so a provider added to the registry shows up in it with no
// second list to remember.
func TestStartupInventoryIsOneLineNamingEveryProvider(t *testing.T) {
	providers := provider.New(ratelimit.NewRegistry(nil))
	resolver := credentials.NewResolver(credentials.Config{
		Source:                emptySource{},
		WalletSyncers:         providers.WalletSyncers(),
		ExchangeSyncers:       providers.ExchangeSyncers(),
		BrokerSyncers:         providers.BrokerSyncers(),
		PriceProviders:        providers.PriceProviders(),
		KeylessWalletSyncers:  providers.KeylessWalletSyncers(),
		KeylessPriceProviders: providers.KeylessPriceProviders(),
	})

	var buf bytes.Buffer
	logProviderInventory(context.Background(), resolver, slog.New(slog.NewJSONHandler(&buf, nil)))

	var line map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &rec))
		if rec["msg"] == "provider inventory without a user" {
			require.Nil(t, line, "the inventory must be one line")
			line = rec
		}
	}
	require.NotNil(t, line, "no inventory line logged")

	got, ok := line["providers"].(map[string]any)
	require.True(t, ok, "providers must be a group keyed by slug: %v", line)
	for _, d := range providers.Descriptors() {
		assert.Contains(t, got, d.Slug)
	}

	// A fresh database: keyless sources resolve, keyed ones say they do not.
	assert.Equal(t, "price=keyless", got["cbr"])
	assert.Equal(t, "wallet=keyless", got["esplora"])
	assert.Equal(t, "price=keyless", got["coingecko"])
	assert.Equal(t, "exchange=per_account price=unresolved", got["binance"])
}
