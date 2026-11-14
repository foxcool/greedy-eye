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
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/adapter/cbr"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackfillCbrRates_RerunCreatesNothing drives the backfill through the
// real handler, the real cbr adapter and this store, against a stub feed that
// answers every date_req with one recorded set — the shape of a weekend, where
// several days are in force under one set.
func TestBackfillCbrRates_RerunCreatesNothing(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	body, err := os.ReadFile(filepath.Join("..", "..", "adapter", "cbr", "testdata", "daily_2024-02-17.xml"))
	require.NoError(t, err)
	var asked []string
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Query().Get("date_req"))
		_, _ = w.Write(body)
	}))
	t.Cleanup(feed.Close)

	forex := func(symbol string) *entity.Asset {
		a, err := s.CreateAsset(ctx, &entity.Asset{Symbol: symbol, Name: symbol, Type: entity.AssetTypeForex, Market: entity.MarketForex})
		require.NoError(t, err)
		return a
	}
	usd, rub, eur := forex("USD"), forex("RUB"), forex("EUR")
	// A token calling itself EUR must not collect the euro's history.
	token, err := s.CreateAsset(ctx, &entity.Asset{Symbol: "EUR", Name: "EUR token", Type: entity.AssetTypeCryptocurrency})
	require.NoError(t, err)

	h := marketdata.NewHandler(s, slog.New(slog.DiscardHandler)).
		WithProvider(cbr.ProviderName, cbr.NewProvider(cbr.NewClient(cbr.Config{BaseURL: feed.URL})))
	admin := middleware.ContextWithUser(ctx, &entity.User{ID: "admin-1", Roles: []string{"admin"}})
	backfill := func() *apiv1.BackfillCbrRatesResponse {
		t.Helper()
		resp, err := h.BackfillCbrRates(admin, connect.NewRequest(&apiv1.BackfillCbrRatesRequest{
			FromDate: "2024-02-16", ToDate: "2024-02-19",
		}))
		require.NoError(t, err)
		require.Empty(t, resp.Msg.Errors)
		return resp.Msg
	}

	first := backfill()
	assert.Equal(t, []string{"16/02/2024", "17/02/2024", "18/02/2024", "19/02/2024"}, asked)
	assert.Equal(t, uint32(4), first.DaysRequested)
	// Four days, one set: a row per asset, not per day.
	assert.Equal(t, uint32(2), first.PricesCreated)
	assert.Equal(t, uint32(0), first.PricesHeld)

	again := backfill()
	assert.Equal(t, uint32(0), again.PricesCreated, "re-running the same range creates nothing")
	assert.Equal(t, uint32(2), again.PricesHeld)

	setDate := time.Date(2024, time.February, 17, 0, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	history := func(assetID string) []*entity.StoredPrice {
		t.Helper()
		rows, _, err := s.ListPriceHistory(ctx, marketdata.ListPriceHistoryOpts{
			AssetID: assetID, BaseAssetID: usd.ID, SourceID: cbr.ProviderName, PageSize: 100,
		})
		require.NoError(t, err)
		return rows
	}
	rubRows := history(rub.ID)
	require.Len(t, rubRows, 1)
	assert.True(t, rubRows[0].Timestamp.Equal(setDate), "dated by the set, got %s", rubRows[0].Timestamp)
	// 1 / 92.5492 USD per rouble, scaled by 8 decimals.
	assert.Equal(t, "1080506", rubRows[0].Last.String())
	assert.Len(t, history(eur.ID), 1)
	assert.Empty(t, history(token.ID))
}
