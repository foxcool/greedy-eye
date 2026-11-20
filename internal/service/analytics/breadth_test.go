package analytics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// The fixture holds 2 ETH (4000 USD, +5.263%) and 0.5 BTC (20000 USD, -4.762%).
func TestGetHeatmap_Breadth(t *testing.T) {
	const ethPct, btcPct = 5.2632, -4.7619
	// Value-weighted: 4000 and 20000 USD of the 24000 that has history.
	bothWeighted := (4000*ethPct + 20000*btcPct) / 24000

	type leaf struct {
		nodeID string
		pct    float64
		stale  bool
	}
	tests := []struct {
		name    string
		mutate  func(*fakeStore, *fakeMD)
		groupBy apiv1.HeatmapGroupBy

		up, priced, unpriced, noHistory, stale uint32
		weighted                               *float64
		best, worst                            *leaf
	}{
		{
			name: "every holding priced with history",
			up:   1, priced: 2,
			weighted: &bothWeighted,
			best:     &leaf{nodeID: "eth", pct: ethPct},
			worst:    &leaf{nodeID: "btc", pct: btcPct},
		},
		{
			name: "a holding with no price stays out of the denominator",
			mutate: func(st *fakeStore, md *fakeMD) {
				st.holdings = append(st.holdings, &entity.Holding{
					ID: "h3", AssetID: "doge", AccountID: "a1", PortfolioID: "p1",
					Amount: dec("1000"), Decimals: 0,
				})
			},
			up: 1, priced: 2, unpriced: 1,
			weighted: &bothWeighted,
			best:     &leaf{nodeID: "eth", pct: ethPct},
			worst:    &leaf{nodeID: "btc", pct: btcPct},
		},
		{
			name: "a holding with no history is priced but has no move",
			mutate: func(_ *fakeStore, md *fakeMD) {
				delete(md.hist, "eth|USD")
			},
			up: 0, priced: 2, noHistory: 1,
			weighted: ptr(btcPct),
			best:     &leaf{nodeID: "btc", pct: btcPct},
			worst:    &leaf{nodeID: "btc", pct: btcPct},
		},
		{
			name: "no price and no history together",
			mutate: func(st *fakeStore, md *fakeMD) {
				st.holdings = append(st.holdings, &entity.Holding{
					ID: "h3", AssetID: "doge", AccountID: "a1", PortfolioID: "p1",
					Amount: dec("1000"), Decimals: 0,
				})
				delete(md.hist, "btc|USD")
			},
			up: 1, priced: 2, unpriced: 1, noHistory: 1,
			weighted: ptr(ethPct),
			best:     &leaf{nodeID: "eth", pct: ethPct},
			worst:    &leaf{nodeID: "eth", pct: ethPct},
		},
		{
			name: "a stale quote is counted with its last change and flagged",
			mutate: func(_ *fakeStore, md *fakeMD) {
				md.latest["eth|USD"] = observedAt(price("eth", "USD", "200000", 2), time.Now().Add(-30*24*time.Hour))
			},
			up: 1, priced: 2, stale: 1,
			weighted: &bothWeighted,
			best:     &leaf{nodeID: "eth", pct: ethPct, stale: true},
			worst:    &leaf{nodeID: "btc", pct: btcPct},
		},
		{
			name: "nothing with history leaves the average and extremes unset",
			mutate: func(_ *fakeStore, md *fakeMD) {
				md.hist = nil
			},
			priced: 2, noHistory: 2,
		},
		{
			name: "nothing priced",
			mutate: func(_ *fakeStore, md *fakeMD) {
				md.latest = nil
			},
			unpriced: 2,
		},
		{
			name:    "grouped extremes name the leaf node, not the asset",
			groupBy: apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT,
			up:      1, priced: 2,
			weighted: &bothWeighted,
			best:     &leaf{nodeID: "a1:eth", pct: ethPct},
			worst:    &leaf{nodeID: "a2:btc", pct: btcPct},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, md := fixture()
			if tt.mutate != nil {
				tt.mutate(st, md)
			}
			h := NewHandler(st.client(), testLogger()).WithMarketDataClient(md)
			resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest(func(m *apiv1.GetHeatmapRequest) {
				m.GroupBy = tt.groupBy
			}))
			require.NoError(t, err)

			b := resp.Msg.GetBreadth()
			require.NotNil(t, b)
			assert.Equal(t, tt.up, b.GetUpCount(), "up")
			assert.Equal(t, tt.priced, b.GetPricedCount(), "priced")
			assert.Equal(t, tt.unpriced, b.GetUnpricedCount(), "unpriced")
			assert.Equal(t, tt.noHistory, b.GetNoHistoryCount(), "no history")
			assert.Equal(t, tt.stale, b.GetStaleCount(), "stale")
			// The strip and the coverage block state one denominator.
			assert.Equal(t, resp.Msg.GetCoverage().GetPricedCount(), b.GetPricedCount())
			assert.Equal(t, resp.Msg.GetCoverage().GetUnpricedCount(), b.GetUnpricedCount())

			if tt.weighted == nil {
				assert.Nil(t, b.WeightedChangePct)
			} else {
				require.NotNil(t, b.WeightedChangePct)
				assert.InDelta(t, *tt.weighted, b.GetWeightedChangePct(), 1e-3)
			}
			assertLeaf := func(what string, want *leaf, got *apiv1.HeatmapBreadthLeaf) {
				if want == nil {
					assert.Nil(t, got, what)
					return
				}
				require.NotNil(t, got, what)
				assert.Equal(t, want.nodeID, got.GetNodeId(), what)
				assert.InDelta(t, want.pct, got.GetChangePct(), 1e-3, what)
				assert.Equal(t, want.stale, got.GetStale(), what)
				assert.NotEmpty(t, got.GetLabel(), what)
				assert.NotEmpty(t, got.GetAssetId(), what)
			}
			assertLeaf("best", tt.best, b.GetBest())
			assertLeaf("worst", tt.worst, b.GetWorst())
		})
	}
}

// Two positions in one asset are two holdings in the ratio, as in coverage.
func TestGetHeatmap_BreadthCountsHoldingsNotAssets(t *testing.T) {
	st, md := fixture()
	st.holdings = append(st.holdings, &entity.Holding{
		ID: "h3", AssetID: "eth", AccountID: "a2", PortfolioID: "p1",
		Amount: dec("1000000000000000000"), Decimals: 18,
	})
	h := NewHandler(st.client(), testLogger()).WithMarketDataClient(md)
	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)

	b := resp.Msg.GetBreadth()
	assert.Equal(t, uint32(2), b.GetUpCount())
	assert.Equal(t, uint32(3), b.GetPricedCount())
}

func ptr(f float64) *float64 { return &f }

// Leaves are built from a map, so equal leaves arrive in any order; the
// extreme must not depend on it.
func TestGetHeatmap_BreadthExtremeTieIsDeterministic(t *testing.T) {
	st, md := fixture()
	// The same ETH amount in both accounts: two leaves with equal size and change.
	st.holdings = []*entity.Holding{
		{ID: "h1", AssetID: "eth", AccountID: "a2", PortfolioID: "p1", Amount: dec("1000000000000000000"), Decimals: 18},
		{ID: "h2", AssetID: "eth", AccountID: "a1", PortfolioID: "p1", Amount: dec("1000000000000000000"), Decimals: 18},
	}
	h := NewHandler(st.client(), testLogger()).WithMarketDataClient(md)
	for range 50 {
		resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest(func(m *apiv1.GetHeatmapRequest) {
			m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT
		}))
		require.NoError(t, err)
		require.Equal(t, "a1:eth", resp.Msg.GetBreadth().GetBest().GetNodeId())
		require.Equal(t, "a1:eth", resp.Msg.GetBreadth().GetWorst().GetNodeId())
	}
}
