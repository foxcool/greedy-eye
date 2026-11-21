//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/entity/entitytest"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pins that a market read writes no asset rows, through the real handlers,
// resolver and adapter against a recorded ranking.
func TestMarketHeatmapLeavesTheCatalogueAlone(t *testing.T) {
	pool := getTestPool(t)
	ctx := context.Background()

	body, err := os.ReadFile(filepath.Join("..", "..", "adapter", "coingecko", "testdata", "coins_markets_top.json"))
	require.NoError(t, err)
	var calls int
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write(body)
	}))
	t.Cleanup(feed.Close)

	mdStore := NewMarketDataStore(pool)
	pfStore := NewPortfolioStore(pool)
	user := createTestUser(t, NewUserStore(pool))
	_, err = mdStore.CreateAsset(ctx, &entity.Asset{Symbol: "USD", Name: "US Dollar", Type: entity.AssetTypeForex, Market: entity.MarketForex})
	require.NoError(t, err)
	btc, err := mdStore.CreateAsset(ctx, &entity.Asset{Symbol: "BTC", Name: "Bitcoin", Type: entity.AssetTypeCryptocurrency})
	require.NoError(t, err)
	account, err := pfStore.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "cold", Type: entity.AccountTypeManual})
	require.NoError(t, err)
	_, err = pfStore.CreateHolding(ctx, entitytest.Holding(entity.Holding{
		AssetID: btc.ID, AccountID: account.ID, Amount: decimal.RequireFromString("50000000"), Decimals: 8,
	}, entitytest.HoldingState{Source: entity.SourceManual}))
	require.NoError(t, err)

	countAssets := func() int {
		t.Helper()
		var n int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM assets").Scan(&n))
		return n
	}
	before := countAssets()

	log := slog.New(slog.DiscardHandler)
	resolver := credentials.NewResolver(credentials.Config{
		Source: pfStore,
		KeylessPriceProviders: map[string]marketdata.PriceProvider{
			coingecko.ProviderName: coingecko.NewProvider(coingecko.NewClient(coingecko.Config{BaseURL: feed.URL})),
		},
		Log: log,
	})
	md := marketdata.NewHandler(mdStore, log)
	pf := portfolio.NewHandler(pfStore, log).WithMarketDataClient(md)
	h := analytics.NewHandler(pf, log).WithMarketDataClient(md).WithMarketRankingSource(resolver)

	caller := middleware.ContextWithUser(ctx, user)
	resp, err := h.GetHeatmap(caller, connect.NewRequest(&apiv1.GetHeatmapRequest{Scope: apiv1.HeatmapScope_HEATMAP_SCOPE_MARKET}))
	require.NoError(t, err)

	assert.Equal(t, 1, calls)
	require.Len(t, resp.Msg.Nodes, 4, "the unranked listing is not drawn")
	assert.Equal(t, int32(1), resp.Msg.Nodes[0].GetRank())
	assert.True(t, resp.Msg.Nodes[0].Held, "the BTC holding marks the BTC tile")
	assert.Equal(t, btc.ID, resp.Msg.Nodes[0].AssetId)
	assert.NotNil(t, resp.Msg.SourceReadAt)

	assert.Equal(t, before, countAssets(), "a market fetch writes no asset rows")
}
