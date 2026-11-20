package coingecko

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/spend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedMarkets serves testdata/coins_markets_top.json and records every
// request it saw.
func recordedMarkets(t *testing.T) (*Provider, *[]*http.Request) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "coins_markets_top.json"))
	require.NoError(t, err)
	var seen []*http.Request
	p := serveProvider(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		_, _ = w.Write(body)
	})
	return p, &seen
}

// TestTopByMarketCap_OneRequest: rank, capitalisation, volume and all three
// windows come from one /coins/markets page, so the cost of a map is one
// request whatever its size and window.
func TestTopByMarketCap_OneRequest(t *testing.T) {
	p, seen := recordedMarkets(t)

	before := time.Now()
	got, err := p.TopByMarketCap(context.Background(), 5)
	require.NoError(t, err)

	require.Len(t, *seen, 1, "one request per ranking read")
	r := (*seen)[0]
	assert.Equal(t, "/coins/markets", r.URL.Path)
	q := r.URL.Query()
	assert.Equal(t, "usd", q.Get("vs_currency"))
	assert.Equal(t, "market_cap_desc", q.Get("order"))
	assert.Equal(t, "5", q.Get("per_page"))
	assert.Equal(t, "1", q.Get("page"))
	assert.Equal(t, "24h,7d,30d", q.Get("price_change_percentage"))
	assert.Empty(t, q.Get("ids"), "the ranking is the market's, not a list we name")

	assert.Equal(t, ProviderName, got.Source)
	assert.Equal(t, "usd", got.Currency)
	assert.False(t, got.ReadAt.Before(before), "read time is when the source answered")

	// The unranked fresh listing is not part of a ranking.
	require.Len(t, got.Coins, 4)
	btc := got.Coins[0]
	assert.Equal(t, "bitcoin", btc.CoinID)
	assert.Equal(t, "BTC", btc.Symbol)
	assert.Equal(t, "Bitcoin", btc.Name)
	assert.Equal(t, 1, btc.Rank)
	assert.InDelta(t, 91234.5, btc.Price, 1e-9)
	assert.InDelta(t, 1.81e12, btc.MarketCap, 1)
	assert.InDelta(t, 4.821e10, btc.Volume24h, 1)
	require.NotNil(t, btc.Change24h)
	require.NotNil(t, btc.Change7d)
	require.NotNil(t, btc.Change30d)
	assert.InDelta(t, -0.8826, *btc.Change24h, 1e-9)
	assert.InDelta(t, 3.4102, *btc.Change7d, 1e-9)
	assert.InDelta(t, -12.0517, *btc.Change30d, 1e-9)

	sol := got.Coins[3]
	assert.Equal(t, 4, sol.Rank)
	assert.Nil(t, sol.Change30d, "a window the source did not report stays absent, not zero")
}

func TestTopByMarketCap_RejectsSizeOutsidePage(t *testing.T) {
	p, seen := recordedMarkets(t)
	for _, n := range []int{0, marketsPageSize + 1} {
		_, err := p.TopByMarketCap(context.Background(), n)
		assert.Error(t, err, "n=%d", n)
	}
	assert.Empty(t, *seen, "a size one page cannot carry is refused before any request")
}

// TestTopByMarketCap_RejectedKey: a dead key is reported as such, so the
// caller can move to another one.
func TestTopByMarketCap_RejectedKey(t *testing.T) {
	p := serveProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := p.TopByMarketCap(context.Background(), 5)
	require.Error(t, err)
	assert.True(t, spend.KeyRefused(err))
}

// TestRankedCoinID: a catalogue asset is a ranked coin only under the identity
// it is priced by — the curated id of the global crypto market. A contract row
// carrying a famous ticker is not that coin.
func TestRankedCoinID(t *testing.T) {
	p := NewProvider(NewClient(Config{}))
	tests := []struct {
		name  string
		asset *entity.Asset
		want  string
		ok    bool
	}{
		{"curated coin", &entity.Asset{Symbol: "BTC", Market: entity.MarketCrypto}, "bitcoin", true},
		{"symbol case does not matter", &entity.Asset{Symbol: "eth", Market: entity.MarketCrypto}, "ethereum", true},
		{"contract row with a famous ticker", &entity.Asset{Symbol: "USDT", Market: "onchain:eth/0xdead"}, "", false},
		{"equity with a crypto ticker", &entity.Asset{Symbol: "SOL", Market: "moex"}, "", false},
		{"symbol outside the curated map", &entity.Asset{Symbol: "ZZZ", Market: entity.MarketCrypto}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := p.RankedCoinID(tt.asset)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

var _ entity.MarketRanker = (*Provider)(nil)
