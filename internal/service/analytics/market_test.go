package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/spend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f64(v float64) *float64 { return &v }

// fakeRanker serves one ranking and counts how often the source was asked.
type fakeRanker struct {
	ranking entity.MarketRanking
	err     error
	calls   int
	asked   []int
}

func (f *fakeRanker) TopByMarketCap(_ context.Context, n int) (entity.MarketRanking, error) {
	f.calls++
	f.asked = append(f.asked, n)
	if f.err != nil {
		return entity.MarketRanking{}, f.err
	}
	return f.ranking, nil
}

// RankedCoinID knows the curated coins of the fixture, on the crypto market
// only, the way the real provider does.
func (f *fakeRanker) RankedCoinID(a *entity.Asset) (string, bool) {
	if a.Market != entity.MarketCrypto {
		return "", false
	}
	id, ok := map[string]string{"BTC": "bitcoin", "ETH": "ethereum"}[a.Symbol]
	return id, ok
}

type fakeRankingSource struct{ rankers []entity.MarketRanker }

func (f *fakeRankingSource) MarketRankersFor(context.Context, string) ([]entity.MarketRanker, error) {
	return f.rankers, nil
}

var rankingReadAt = time.Date(2026, 11, 20, 19, 58, 31, 0, time.UTC)

// marketRanking: BTC, ETH and SOL by rank; SOL has no 30d history.
func marketRanking() entity.MarketRanking {
	return entity.MarketRanking{
		Source:   "coingecko",
		Currency: "usd",
		ReadAt:   rankingReadAt,
		Coins: []entity.RankedCoin{
			{CoinID: "bitcoin", Symbol: "BTC", Name: "Bitcoin", Rank: 1, Price: 91234.5, MarketCap: 1.81e12, Volume24h: 4.8e10,
				Change24h: f64(-0.88), Change7d: f64(3.41), Change30d: f64(-12.05)},
			{CoinID: "ethereum", Symbol: "ETH", Name: "Ethereum", Rank: 2, Price: 3021.17, MarketCap: 3.645e11, Volume24h: 2.3e10,
				Change24h: f64(1.38), Change7d: f64(5.02), Change30d: f64(-18.44)},
			{CoinID: "solana", Symbol: "SOL", Name: "Solana", Rank: 4, Price: 141.88, MarketCap: 7.84e10, Volume24h: 5.1e9,
				Change24h: f64(-2.67), Change7d: f64(-6.19)},
		},
	}
}

// marketFixture is fixture() with the held assets on the crypto market, so
// the ranker can recognise them.
func marketFixture() (*fakeStore, *fakeMD) {
	st, md := fixture()
	md.assets["btc"].Market = strPtr(entity.MarketCrypto)
	md.assets["eth"].Market = strPtr(entity.MarketCrypto)
	return st, md
}

func marketHandler(st *fakeStore, md *fakeMD, rankers ...entity.MarketRanker) *Handler {
	return NewHandler(st.client(), testLogger()).
		WithMarketDataClient(md).
		WithMarketRankingSource(&fakeRankingSource{rankers: rankers})
}

func marketRequest(mut ...func(*apiv1.GetHeatmapRequest)) *connect.Request[apiv1.GetHeatmapRequest] {
	msg := &apiv1.GetHeatmapRequest{Scope: apiv1.HeatmapScope_HEATMAP_SCOPE_MARKET}
	for _, m := range mut {
		m(msg)
	}
	return connect.NewRequest(msg)
}

func TestGetHeatmap_MarketTopByRank(t *testing.T) {
	st, md := marketFixture()
	ranker := &fakeRanker{ranking: marketRanking()}
	h := marketHandler(st, md, ranker)

	resp, err := h.GetHeatmap(userCtx("u1"), marketRequest())
	require.NoError(t, err)

	assert.Equal(t, []int{marketTopN}, ranker.asked, "one read of the top N")
	assert.Equal(t, "USD", resp.Msg.QuoteAssetId, "the ranking is quoted in USD")
	assert.Equal(t, "coingecko", resp.Msg.Source)
	require.NotNil(t, resp.Msg.SourceReadAt)
	assert.True(t, resp.Msg.SourceReadAt.AsTime().Equal(rankingReadAt), "the read time is the source's, not the request's")
	require.NotNil(t, resp.Msg.CalculatedAt)

	require.Len(t, resp.Msg.Nodes, 3)
	btc, eth, sol := resp.Msg.Nodes[0], resp.Msg.Nodes[1], resp.Msg.Nodes[2]

	assert.Equal(t, "coingecko:bitcoin", btc.Id)
	assert.Equal(t, "BTC", btc.Label)
	assert.Empty(t, btc.ParentId)
	require.NotNil(t, btc.Rank)
	assert.Equal(t, int32(1), *btc.Rank)
	assert.InDelta(t, 1.81e12, btc.Size, 1, "size defaults to capitalisation")
	assert.InDelta(t, -0.88, btc.ColorValue, 1e-9, "color defaults to 24h change")
	require.NotNil(t, btc.Price)
	assert.InDelta(t, 91234.5, *btc.Price, 1e-9)
	assert.True(t, btc.Held)
	assert.Equal(t, "btc", btc.AssetId, "a held tile links to the caller's asset")

	assert.Equal(t, int32(2), eth.GetRank())
	assert.True(t, eth.Held)
	assert.Equal(t, "eth", eth.AssetId)

	assert.Equal(t, int32(4), sol.GetRank())
	assert.False(t, sol.Held)
	assert.Empty(t, sol.AssetId, "an unheld tile links nowhere")
}

func TestGetHeatmap_MarketSizeAndWindow(t *testing.T) {
	st, md := marketFixture()
	h := marketHandler(st, md, &fakeRanker{ranking: marketRanking()})

	resp, err := h.GetHeatmap(userCtx("u1"), marketRequest(func(m *apiv1.GetHeatmapRequest) {
		m.SizeMetric = apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VOLUME
		m.Window = apiv1.HeatmapWindow_HEATMAP_WINDOW_30D
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Nodes, 3)
	byID := map[string]*apiv1.HeatmapNode{}
	for _, n := range resp.Msg.Nodes {
		byID[n.Id] = n
	}
	assert.InDelta(t, 4.8e10, byID["coingecko:bitcoin"].Size, 1)
	assert.InDelta(t, -12.05, byID["coingecko:bitcoin"].ColorValue, 1e-9)
	assert.InDelta(t, 0, byID["coingecko:solana"].ColorValue, 1e-9, "no history: flat, as on the portfolio map")

	resp, err = h.GetHeatmap(userCtx("u1"), marketRequest(func(m *apiv1.GetHeatmapRequest) {
		m.Window = apiv1.HeatmapWindow_HEATMAP_WINDOW_7D
	}))
	require.NoError(t, err)
	assert.InDelta(t, 3.41, resp.Msg.Nodes[0].ColorValue, 1e-9)
}

// TestGetHeatmap_MarketSourceCallsPerTTL: the source is asked at most once per
// TTL whatever window, size or caller is drawn, so the plan cost of the map is
// bounded by time, not by traffic.
func TestGetHeatmap_MarketSourceCallsPerTTL(t *testing.T) {
	st, md := marketFixture()
	st.portfolios["p9"] = &entity.Portfolio{ID: "p9", UserID: "u2", Name: "other"}
	ranker := &fakeRanker{ranking: marketRanking()}
	h := marketHandler(st, md, ranker)
	now := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	h.market.now = func() time.Time { return now }

	draws := []func(*apiv1.GetHeatmapRequest){
		func(*apiv1.GetHeatmapRequest) {},
		func(m *apiv1.GetHeatmapRequest) { m.Window = apiv1.HeatmapWindow_HEATMAP_WINDOW_7D },
		func(m *apiv1.GetHeatmapRequest) {
			m.SizeMetric = apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VOLUME
		},
	}
	for _, user := range []string{"u1", "u2"} {
		for _, d := range draws {
			_, err := h.GetHeatmap(userCtx(user), marketRequest(d))
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, ranker.calls, "six draws inside the TTL cost one source call")

	now = now.Add(marketTTL)
	_, err := h.GetHeatmap(userCtx("u1"), marketRequest())
	require.NoError(t, err)
	assert.Equal(t, 2, ranker.calls, "an expired read is refreshed once")
}

// TestGetHeatmap_MarketFailoverOnRefusedKey: a spent or rejected key hands the
// read to the next one; any other failure is the source's answer.
func TestGetHeatmap_MarketFailoverOnRefusedKey(t *testing.T) {
	st, md := marketFixture()
	spent := &fakeRanker{err: spend.ErrQuotaExhausted}
	next := &fakeRanker{ranking: marketRanking()}
	h := marketHandler(st, md, spent, next)

	resp, err := h.GetHeatmap(userCtx("u1"), marketRequest())
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Nodes, 3)
	assert.Equal(t, 1, spent.calls)
	assert.Equal(t, 1, next.calls)

	down := &fakeRanker{err: errors.New("unexpected status 503 from CoinGecko")}
	spare := &fakeRanker{ranking: marketRanking()}
	h = marketHandler(st, md, down, spare)
	_, err = h.GetHeatmap(userCtx("u1"), marketRequest())
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	assert.Zero(t, spare.calls, "an outage is not a reason to spend another plan")

	// A failure is not remembered: the next request asks again.
	down.err = nil
	down.ranking = marketRanking()
	_, err = h.GetHeatmap(userCtx("u1"), marketRequest())
	require.NoError(t, err)
	assert.Equal(t, 2, down.calls)
}

// TestGetHeatmap_MarketHeldIgnoresExcluded: a quarantined holding is not a
// position, so it does not mark the coin as held.
func TestGetHeatmap_MarketHeldIgnoresExcluded(t *testing.T) {
	st, md := marketFixture()
	st.holdings[0].SetExcluded(true, entity.ExclusionUnrecorded) // the ETH holding
	h := marketHandler(st, md, &fakeRanker{ranking: marketRanking()})

	resp, err := h.GetHeatmap(userCtx("u1"), marketRequest())
	require.NoError(t, err)
	for _, n := range resp.Msg.Nodes {
		if n.Id == "coingecko:ethereum" {
			assert.False(t, n.Held)
			assert.Empty(t, n.AssetId)
		}
	}
}

func TestGetHeatmap_MarketValidation(t *testing.T) {
	st, md := marketFixture()
	h := marketHandler(st, md, &fakeRanker{ranking: marketRanking()})

	tests := []struct {
		name string
		ctx  context.Context
		mut  func(*apiv1.GetHeatmapRequest)
		code connect.Code
	}{
		{"scope_id must be empty", userCtx("u1"), func(m *apiv1.GetHeatmapRequest) { m.ScopeId = "p1" }, connect.CodeInvalidArgument},
		{"holding value is not a market size", userCtx("u1"), func(m *apiv1.GetHeatmapRequest) {
			m.SizeMetric = apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VALUE
		}, connect.CodeInvalidArgument},
		{"pnl color not implemented", userCtx("u1"), func(m *apiv1.GetHeatmapRequest) {
			m.ColorMetric = apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_PNL_PCT
		}, connect.CodeUnimplemented},
		{"grouping not implemented", userCtx("u1"), func(m *apiv1.GetHeatmapRequest) {
			m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_SECTOR
		}, connect.CodeUnimplemented},
		{"only USD", userCtx("u1"), func(m *apiv1.GetHeatmapRequest) { m.QuoteAssetId = "RUB" }, connect.CodeInvalidArgument},
		{"caller required", context.Background(), func(*apiv1.GetHeatmapRequest) {}, connect.CodeUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.GetHeatmap(tt.ctx, marketRequest(tt.mut))
			require.Error(t, err)
			assert.Equal(t, tt.code, connect.CodeOf(err))
		})
	}

	// Without a ranking source the scope stays what it was.
	plain := NewHandler(st.client(), testLogger()).WithMarketDataClient(md)
	_, err := plain.GetHeatmap(userCtx("u1"), marketRequest())
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	// No key can rank: a precondition of the instance, not an outage.
	empty := marketHandler(st, md)
	_, err = empty.GetHeatmap(userCtx("u1"), marketRequest())
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
