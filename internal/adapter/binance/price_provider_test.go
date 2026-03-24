package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a Client pointing to the given test server URL.
func newTestClient(serverURL string) *Client {
	c := NewClient(Config{})
	c.baseURL = serverURL
	return c
}

func testAssets() []*entity.Asset {
	return []*entity.Asset{
		{ID: "uuid-btc", Symbol: "BTC"},
		{ID: "uuid-eth", Symbol: "ETH"},
	}
}

func TestFetchPrices_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/ticker/price", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]TickerPrice{
			{Symbol: "BTCUSDT", Price: "67000.12345678"},
			{Symbol: "ETHUSDT", Price: "3500.00000000"},
		})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))

	prices, err := p.FetchPrices(context.Background(), testAssets())
	require.NoError(t, err)
	require.Len(t, prices, 2)

	byAsset := make(map[string]int64, 2)
	for _, sp := range prices {
		assert.Equal(t, "binance", sp.SourceID)
		assert.Equal(t, "usdt", sp.BaseAssetID)
		assert.Equal(t, "latest", sp.Interval)
		assert.Equal(t, uint32(8), sp.Decimals)
		byAsset[sp.AssetID] = sp.Last
	}
	assert.Equal(t, int64(6700012345678), byAsset["uuid-btc"])
	assert.Equal(t, int64(350000000000), byAsset["uuid-eth"])
}

func TestFetchPrices_EmptyAssets(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))

	prices, err := p.FetchPrices(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.False(t, called, "no HTTP call expected for empty assets")
}

func TestFetchPrices_BinanceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))

	_, err := p.FetchPrices(context.Background(), testAssets())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binance")
}

func TestGetTickerPrices_BatchRequest(t *testing.T) {
	var capturedURL *url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]TickerPrice{})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.GetTickerPrices(context.Background(), []string{"BTCUSDT", "ETHUSDT"})
	require.NoError(t, err)

	require.NotNil(t, capturedURL)
	symbolsParam := capturedURL.Query().Get("symbols")
	assert.True(t, strings.Contains(symbolsParam, "BTCUSDT"), "BTCUSDT should be in symbols param")
	assert.True(t, strings.Contains(symbolsParam, "ETHUSDT"), "ETHUSDT should be in symbols param")
}
