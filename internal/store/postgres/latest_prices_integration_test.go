//go:build integration

package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// The set read behind GetPrices must answer every latest lookup exactly as
// GetLatestPrice answers it alone, on the same rows: a batch that priced an
// asset differently would be a second pricing rule.
func TestLatestPrices_MatchesGetLatestPrice(t *testing.T) {
	ctx := context.Background()
	s := NewMarketDataStore(getTestPool(t))
	h := marketdata.NewHandler(s, slog.New(slog.DiscardHandler))

	usd := createTestAsset(t, s, "Set USD")
	btc := createTestAsset(t, s, "Set BTC")
	eth := createTestAsset(t, s, "Set ETH")
	sol := createTestAsset(t, s, "Set SOL")
	ghost := createTestAsset(t, s, "Set Ghost")

	t0 := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	put := func(asset, base *entity.Asset, source string, at time.Duration, last int64) {
		t.Helper()
		_, err := s.CreatePrice(ctx, &entity.StoredPrice{
			SourceID: source, AssetID: asset.ID, BaseAssetID: base.ID, Interval: "latest",
			Decimals: 2, Last: decimal.NewFromInt(last), Timestamp: t0.Add(at),
		})
		require.NoError(t, err)
	}
	// eth: newest print is in BTC, from another source than its newest USD one.
	put(eth, usd, "exchange1", 1*time.Minute, 200000)
	put(eth, usd, "exchange2", 3*time.Minute, 201000)
	put(eth, btc, "exchange1", 5*time.Minute, 5)
	put(eth, usd, "exchange1", 2*time.Minute, 199000)
	// sol: USD only, several sources.
	put(sol, usd, "exchange2", 4*time.Minute, 15000)
	put(sol, usd, "aggregator", 6*time.Minute, 15100)
	// btc/usd and its inverse, as a cross rate is read.
	put(btc, usd, "exchange1", 7*time.Minute, 6000000)
	put(usd, btc, "exchange1", 8*time.Minute, 1)

	var lookups []*apiv1.PriceLookup
	for _, a := range []string{usd.ID, btc.ID, eth.ID, sol.ID, ghost.ID, uuid.NewString()} {
		lookups = append(lookups, &apiv1.PriceLookup{AssetId: a})
		for _, b := range []string{usd.ID, btc.ID, eth.ID} {
			lookups = append(lookups, &apiv1.PriceLookup{AssetId: a, BaseAssetId: b})
		}
	}
	// By ticker too: the set path resolves tickers as the single one does.
	lookups = append(lookups,
		&apiv1.PriceLookup{AssetId: eth.Symbol, BaseAssetId: usd.Symbol},
		&apiv1.PriceLookup{AssetId: "NO-SUCH-TICKER"})

	batch, err := h.GetPrices(ctx, connect.NewRequest(&apiv1.GetPricesRequest{Lookups: lookups}))
	require.NoError(t, err)
	require.Len(t, batch.Msg.Results, len(lookups))

	priced := 0
	for i, l := range lookups {
		name := fmt.Sprintf("%d %s/%s", i, l.AssetId, l.BaseAssetId)
		single, err := h.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
			AssetId: l.AssetId, BaseAssetId: l.BaseAssetId,
		}))
		got := batch.Msg.Results[i].GetPrice()
		if connect.CodeOf(err) == connect.CodeNotFound {
			assert.Nil(t, got, name)
			continue
		}
		require.NoError(t, err, name)
		priced++
		assert.True(t, proto.Equal(single.Msg, got), "%s\nsingle: %v\nbatch:  %v", name, single.Msg, got)
	}
	// The fixture must exercise both answers, or equality proves nothing.
	assert.Equal(t, 10, priced, "priced lookups in the fixture")
}
