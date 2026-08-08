package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
)

// fakeSettings serves the stored valuation policy; an empty value means the key
// was never written.
type fakeSettings struct{ value string }

func (f *fakeSettings) GetSetting(_ context.Context, _ *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error) {
	if f.value == "" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("setting not found"))
	}
	return connect.NewResponse(&apiv1.GetSettingResponse{
		Setting: &apiv1.Setting{Key: pricefresh.SettingKey, Value: f.value},
	}), nil
}

func observedAt(p *apiv1.Price, at time.Time) *apiv1.Price {
	p.Timestamp = timestamppb.New(at)
	return p
}

// A map is where staleness hides best: a tile drawn from a three-year-old print
// looks exactly like one drawn this morning, and there is no empty rectangle to
// notice the way a total has a missing line.
func TestHeatmap_ReportsStaleQuotes(t *testing.T) {
	st, md := fixture()
	stale := time.Now().Add(-67 * 24 * time.Hour)
	md.latest["eth|USD"] = observedAt(price("eth", "USD", "200000", 2), stale)
	md.latest["btc|USD"] = observedAt(price("btc", "USD", "4000000", 2), time.Now().Add(-time.Hour))

	h := NewHandler(st, testLogger()).WithMarketDataClient(md)
	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)

	cov := resp.Msg.Coverage
	assert.Equal(t, uint32(2), cov.GetPricedCount())
	assert.Equal(t, uint32(1), cov.GetStaleCount())
	assert.Len(t, resp.Msg.Nodes, 2, "a stale tile is still drawn: it is labelled, not hidden")
	require.NotNil(t, cov.GetPricesAsOf())
	assert.WithinDuration(t, stale, cov.GetPricesAsOf().AsTime(), time.Second)
}

// Pricing is cached per asset, but the question the count answers is about
// positions: two holdings of one dead security are two positions in question.
func TestHeatmap_StaleCountIsPerHoldingNotPerAsset(t *testing.T) {
	st, md := fixture()
	st.holdings = append(st.holdings, &entity.Holding{
		ID: "h3", AssetID: "eth", AccountID: "a2", PortfolioID: "p1",
		Amount: dec("1000000000000000000"), Decimals: 18,
	})
	stale := time.Now().Add(-30 * 24 * time.Hour)
	md.latest["eth|USD"] = observedAt(price("eth", "USD", "200000", 2), stale)
	md.latest["btc|USD"] = observedAt(price("btc", "USD", "4000000", 2), time.Now())

	h := NewHandler(st, testLogger()).WithMarketDataClient(md)
	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)

	assert.Equal(t, uint32(2), resp.Msg.Coverage.GetStaleCount(),
		"both ETH positions rest on the same stale quote, and both are counted")
}

// The map and the total must answer under the same rules, so the heatmap reads
// the same stored policy.
func TestHeatmap_HonoursTheStoredPolicy(t *testing.T) {
	st, md := fixture()
	md.latest["eth|USD"] = observedAt(price("eth", "USD", "200000", 2), time.Now().Add(-2*time.Hour))
	md.latest["btc|USD"] = observedAt(price("btc", "USD", "4000000", 2), time.Now())

	base := NewHandler(st, testLogger()).WithMarketDataClient(md)

	t.Run("default keeps a two-hour-old quote current", func(t *testing.T) {
		resp, err := base.GetHeatmap(userCtx("u1"), heatmapRequest())
		require.NoError(t, err)
		assert.Zero(t, resp.Msg.Coverage.GetStaleCount())
	})

	t.Run("a one-hour policy makes it stale", func(t *testing.T) {
		h := base.WithSettingsClient(&fakeSettings{value: `{"price_max_age":"1h"}`})
		resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
		require.NoError(t, err)
		assert.Equal(t, uint32(1), resp.Msg.Coverage.GetStaleCount())
	})
}

// The fixture's prices carry no timestamp at all, which is what every existing
// heatmap test builds. Nothing may invent an observation time from that.
func TestHeatmap_QuotesWithoutTimesLeaveTheMapUndated(t *testing.T) {
	st, md := fixture()

	h := NewHandler(st, testLogger()).WithMarketDataClient(md)
	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)

	assert.Zero(t, resp.Msg.Coverage.GetStaleCount())
	assert.Nil(t, resp.Msg.Coverage.GetPricesAsOf())
}
