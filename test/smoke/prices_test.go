//go:build smoke

package smoke_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/foxcool/greedy-eye/api/v1"
	cbradapter "github.com/foxcool/greedy-eye/internal/adapter/cbr"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertPositivePrice parses a raw integer price string and asserts it is > 0.
func assertPositivePrice(t *testing.T, raw, msg string) {
	t.Helper()
	d, err := decimal.NewFromString(raw)
	require.NoError(t, err, "price must be a valid decimal string")
	assert.True(t, d.IsPositive(), msg)
}

// TestFetchExternalPrices_Keyless runs the whole price path — provider
// resolution, fetch, storage, latest-price lookup — through a provider that
// needs no account. Every keyed provider lives in an account since v0.7.0, so a
// key in the environment no longer reaches the server; the Bank of Russia feed
// is the one crypto-free path that works on an empty database.
func TestFetchExternalPrices_Keyless(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	client := newMDClient(smokeTestUserID)

	sym := "EUR"
	createResp, err := client.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{
			Name:   "Euro",
			Symbol: &sym,
			Type:   v1.AssetType_ASSET_TYPE_FOREX,
		},
	}))
	require.NoError(t, err)
	eurID := createResp.Msg.GetId()

	fetchResp, err := client.FetchExternalPrices(ctx, connect.NewRequest(&v1.FetchExternalPricesRequest{
		SourceIds: []string{cbradapter.ProviderName},
		AssetIds:  []string{eurID},
	}))
	require.NoError(t, err)
	assert.Empty(t, fetchResp.Msg.GetErrors(), "expected no fetch errors")
	assert.Greater(t, fetchResp.Msg.GetPricesFetched(), int32(0), "expected at least one price fetched")
	assert.Greater(t, fetchResp.Msg.GetPricesStored(), int32(0), "expected at least one price stored")

	// The feed is republished in USD, so the base resolves by that symbol.
	latestResp, err := client.GetLatestPrice(ctx, connect.NewRequest(&v1.GetLatestPriceRequest{
		AssetId:     eurID,
		BaseAssetId: "USD",
	}))
	require.NoError(t, err)
	assertPositivePrice(t, latestResp.Msg.GetLast(), "EUR/USD price should be > 0")
	assert.Equal(t, cbradapter.ProviderName, latestResp.Msg.GetSourceId())
}
