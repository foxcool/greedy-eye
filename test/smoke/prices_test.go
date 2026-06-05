//go:build smoke

package smoke_test

import (
	"context"
	"os"
	"testing"

	"connectrpc.com/connect"
	binanceadapter "github.com/foxcool/greedy-eye/internal/adapter/binance"
	v1 "github.com/foxcool/greedy-eye/api/v1"
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

// TestFetchExternalPrices_Binance always runs — Binance ticker endpoint is public (no API key).
// Creates a BTC asset, fetches its price from Binance, verifies the price is stored.
func TestFetchExternalPrices_Binance(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	client := newMDClient(smokeTestUserID)

	sym := "BTC"
	createResp, err := client.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{
			Name:   "Bitcoin",
			Symbol: &sym,
			Type:   v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		},
	}))
	require.NoError(t, err)
	btcID := createResp.Msg.GetId()

	fetchResp, err := client.FetchExternalPrices(ctx, connect.NewRequest(&v1.FetchExternalPricesRequest{
		SourceIds: []string{binanceadapter.ProviderName},
		AssetIds:  []string{btcID},
	}))
	require.NoError(t, err)
	assert.Empty(t, fetchResp.Msg.GetErrors(), "expected no fetch errors")
	assert.Greater(t, fetchResp.Msg.GetPricesFetched(), int32(0), "expected at least one price fetched")
	assert.Greater(t, fetchResp.Msg.GetPricesStored(), int32(0), "expected at least one price stored")

	// Binance uses USDT as the quote currency — resolve via symbol "USDT"
	latestResp, err := client.GetLatestPrice(ctx, connect.NewRequest(&v1.GetLatestPriceRequest{
		AssetId:     btcID,
		BaseAssetId: "USDT",
	}))
	require.NoError(t, err)
	assertPositivePrice(t, latestResp.Msg.GetLast(), "BTC price should be > 0")
	assert.Equal(t, binanceadapter.ProviderName, latestResp.Msg.GetSourceId())
}

// TestFetchExternalPrices_CoinGecko requires EYE_COINGECKO_APIKEY to be set.
// Fetches BTC and ETH prices from CoinGecko and verifies storage.
func TestFetchExternalPrices_CoinGecko(t *testing.T) {
	if os.Getenv("EYE_COINGECKO_APIKEY") == "" {
		t.Skip("EYE_COINGECKO_APIKEY not set — skipping CoinGecko price fetch test")
	}

	resetDB(t)
	ctx := context.Background()
	client := newMDClient(smokeTestUserID)

	// CoinGecko provider resolves BTC by symbol via its internal nativeCoinID map.
	// "btc" → "bitcoin" is a hardcoded mapping; no special tags needed.
	btcSym := "BTC"
	createResp, err := client.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{
			Name:   "Bitcoin",
			Symbol: &btcSym,
			Type:   v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		},
	}))
	require.NoError(t, err)
	btcID := createResp.Msg.GetId()

	fetchResp, err := client.FetchExternalPrices(ctx, connect.NewRequest(&v1.FetchExternalPricesRequest{
		SourceIds: []string{"coingecko"},
		AssetIds:  []string{btcID},
	}))
	require.NoError(t, err)
	assert.Empty(t, fetchResp.Msg.GetErrors(), "expected no fetch errors")
	assert.Greater(t, fetchResp.Msg.GetPricesFetched(), int32(0))
	assert.Greater(t, fetchResp.Msg.GetPricesStored(), int32(0))

	latestResp, err := client.GetLatestPrice(ctx, connect.NewRequest(&v1.GetLatestPriceRequest{
		AssetId:     btcID,
		BaseAssetId: "USD",
	}))
	require.NoError(t, err)
	assertPositivePrice(t, latestResp.Msg.GetLast(), "BTC/USD price should be > 0")
	assert.Equal(t, "coingecko", latestResp.Msg.GetSourceId())
}
