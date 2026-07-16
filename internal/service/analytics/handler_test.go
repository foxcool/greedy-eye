package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Fakes ---

type fakeStore struct {
	portfolios map[string]*entity.Portfolio
	accounts   map[string]*entity.Account
	holdings   []*entity.Holding
}

func (f *fakeStore) GetPortfolio(_ context.Context, id string) (*entity.Portfolio, error) {
	if p, ok := f.portfolios[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("portfolio %s: %w", id, store.ErrNotFound)
}

func (f *fakeStore) GetAccount(_ context.Context, id string) (*entity.Account, error) {
	if a, ok := f.accounts[id]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("account %s: %w", id, store.ErrNotFound)
}

func (f *fakeStore) ListHoldings(_ context.Context, opts portfolio.ListHoldingsOpts) ([]*entity.Holding, string, error) {
	var out []*entity.Holding
	for _, h := range f.holdings {
		if opts.PortfolioID != "" && h.PortfolioID != opts.PortfolioID {
			continue
		}
		if opts.UserID != "" && !f.ownedBy(h, opts.UserID) {
			continue
		}
		if opts.HideExcluded && h.Excluded {
			continue
		}
		out = append(out, h)
	}
	return out, "", nil
}

// ownedBy mirrors the real store's user scoping: by the owning portfolio,
// falling back to the account owner for holdings outside any portfolio.
func (f *fakeStore) ownedBy(h *entity.Holding, userID string) bool {
	if h.PortfolioID != "" {
		p, ok := f.portfolios[h.PortfolioID]
		return ok && p.UserID == userID
	}
	a, ok := f.accounts[h.AccountID]
	return ok && a.UserID == userID
}

// fakeMD serves assets and prices from maps keyed by "assetID|baseAssetID";
// latest prices with empty base ("assetID|") answer any-base lookups.
type fakeMD struct {
	assets map[string]*apiv1.Asset
	latest map[string]*apiv1.Price
	hist   map[string]*apiv1.Price
}

func (f *fakeMD) GetAsset(_ context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if a, ok := f.assets[req.Msg.Id]; ok {
		return connect.NewResponse(a), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("asset %s not found", req.Msg.Id))
}

func (f *fakeMD) GetLatestPrice(_ context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	if p, ok := f.latest[req.Msg.AssetId+"|"+req.Msg.BaseAssetId]; ok {
		return connect.NewResponse(p), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no price"))
}

func (f *fakeMD) ListPriceHistory(_ context.Context, req *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	resp := &apiv1.ListPriceHistoryResponse{}
	if p, ok := f.hist[req.Msg.AssetId+"|"+req.Msg.BaseAssetId]; ok {
		resp.Prices = []*apiv1.Price{p}
	}
	return connect.NewResponse(resp), nil
}

// --- Helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func userCtx(userID string) context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: userID})
}

func price(asset, base, last string, decimals uint32) *apiv1.Price {
	return &apiv1.Price{
		AssetId: asset, BaseAssetId: base,
		Last: last, Decimals: decimals,
	}
}

func strPtr(s string) *string { return &s }

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// fixture: user u1 owns portfolio p1 with 2 ETH on account a1 and 0.5 BTC on a2.
// ETH: 2000 USD now, 1900 USD at window start (+5.26%).
// BTC: 40000 USD now, 42000 USD at window start (-4.76%).
func fixture() (*fakeStore, *fakeMD) {
	st := &fakeStore{
		portfolios: map[string]*entity.Portfolio{
			"p1": {ID: "p1", UserID: "u1", Name: "main"},
		},
		accounts: map[string]*entity.Account{
			"a1": {ID: "a1", UserID: "u1", Name: "wallet-1"},
			"a2": {ID: "a2", UserID: "u1", Name: "exchange-1"},
		},
		holdings: []*entity.Holding{
			{ID: "h1", AssetID: "eth", AccountID: "a1", PortfolioID: "p1", Amount: dec("2000000000000000000"), Decimals: 18},
			{ID: "h2", AssetID: "btc", AccountID: "a2", PortfolioID: "p1", Amount: dec("50000000"), Decimals: 8},
		},
	}
	md := &fakeMD{
		assets: map[string]*apiv1.Asset{
			"eth": {Id: "eth", Name: "Ethereum", Symbol: strPtr("ETH")},
			"btc": {Id: "btc", Name: "Bitcoin", Symbol: strPtr("BTC")},
		},
		latest: map[string]*apiv1.Price{
			"eth|USD": price("eth", "USD", "200000", 2),
			"btc|USD": price("btc", "USD", "4000000", 2),
		},
		hist: map[string]*apiv1.Price{
			"eth|USD": price("eth", "USD", "190000", 2),
			"btc|USD": price("btc", "USD", "4200000", 2),
		},
	}
	return st, md
}

func heatmapRequest(mut ...func(*apiv1.GetHeatmapRequest)) *connect.Request[apiv1.GetHeatmapRequest] {
	msg := &apiv1.GetHeatmapRequest{
		Scope:   apiv1.HeatmapScope_HEATMAP_SCOPE_PORTFOLIO,
		ScopeId: "p1",
	}
	for _, m := range mut {
		m(msg)
	}
	return connect.NewRequest(msg)
}

// --- Tests ---

func TestGetHeatmap_FlatPortfolio(t *testing.T) {
	st, md := fixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)

	require.Len(t, resp.Msg.Nodes, 2)
	assert.Equal(t, "USD", resp.Msg.QuoteAssetId)

	// Sorted by size desc: BTC (0.5 * 40000 = 20000) before ETH (2 * 2000 = 4000).
	btcNode, ethNode := resp.Msg.Nodes[0], resp.Msg.Nodes[1]

	assert.Equal(t, "btc", btcNode.Id)
	assert.Equal(t, "BTC", btcNode.Label)
	assert.Equal(t, "btc", btcNode.AssetId)
	assert.Empty(t, btcNode.ParentId)
	assert.InDelta(t, 20000, btcNode.Size, 1e-9)
	assert.InDelta(t, -4.7619, btcNode.ColorValue, 1e-3)
	require.NotNil(t, btcNode.Price)
	assert.InDelta(t, 40000, *btcNode.Price, 1e-9)

	assert.Equal(t, "eth", ethNode.Id)
	assert.Equal(t, "ETH", ethNode.Label)
	assert.InDelta(t, 4000, ethNode.Size, 1e-9)
	assert.InDelta(t, 5.2631, ethNode.ColorValue, 1e-3)
}

func TestGetHeatmap_GroupByAccount(t *testing.T) {
	st, md := fixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest(func(m *apiv1.GetHeatmapRequest) {
		m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT
	}))
	require.NoError(t, err)

	// 2 group nodes + 2 leaves, groups first sorted by size desc.
	require.Len(t, resp.Msg.Nodes, 4)

	g1, g2 := resp.Msg.Nodes[0], resp.Msg.Nodes[1]
	assert.Equal(t, "a2", g1.Id)
	assert.Equal(t, "exchange-1", g1.Label)
	assert.Empty(t, g1.ParentId)
	assert.InDelta(t, 20000, g1.Size, 1e-9)
	assert.InDelta(t, -4.7619, g1.ColorValue, 1e-3) // single child → its color
	assert.Equal(t, "a1", g2.Id)
	assert.Equal(t, "wallet-1", g2.Label)

	leaf1, leaf2 := resp.Msg.Nodes[2], resp.Msg.Nodes[3]
	assert.Equal(t, "a2:btc", leaf1.Id)
	assert.Equal(t, "a2", leaf1.ParentId)
	assert.Equal(t, "a1:eth", leaf2.Id)
	assert.Equal(t, "a1", leaf2.ParentId)
}

func TestGetHeatmap_CrossPricedAsset(t *testing.T) {
	st, md := fixture()
	// wbtc has no USD price: latest in BTC base only, converted via BTC/USD.
	st.holdings = append(st.holdings, &entity.Holding{
		ID: "h3", AssetID: "wbtc", AccountID: "a1", PortfolioID: "p1",
		Amount: dec("100000000"), Decimals: 8, // 1 WBTC
	})
	md.assets["wbtc"] = &apiv1.Asset{Id: "wbtc", Name: "Wrapped BTC", Symbol: strPtr("WBTC")}
	md.latest["wbtc|"] = price("wbtc", "btc", "99000000", 8)  // 0.99 BTC now
	md.hist["wbtc|btc"] = price("wbtc", "btc", "100000000", 8) // 1.00 BTC at window start

	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)
	require.Len(t, resp.Msg.Nodes, 3)

	// 0.99 BTC * 40000 USD = 39600 USD → largest tile.
	wbtc := resp.Msg.Nodes[0]
	assert.Equal(t, "wbtc", wbtc.Id)
	assert.InDelta(t, 39600, wbtc.Size, 1e-9)
	assert.InDelta(t, -1.0, wbtc.ColorValue, 1e-3) // change in BTC terms
}

func TestGetHeatmap_SkipsUnpricedAndExcluded(t *testing.T) {
	st, md := fixture()
	st.holdings = append(st.holdings,
		&entity.Holding{ID: "h3", AssetID: "mystery", AccountID: "a1", PortfolioID: "p1", Amount: dec("100"), Decimals: 0},
		&entity.Holding{ID: "h4", AssetID: "eth", AccountID: "a1", PortfolioID: "p1", Amount: dec("1000000000000000000"), Decimals: 18, Excluded: true},
	)

	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)
	require.Len(t, resp.Msg.Nodes, 2) // mystery skipped, excluded ETH not counted

	assert.InDelta(t, 4000, resp.Msg.Nodes[1].Size, 1e-9) // still 2 ETH, not 3
}

func TestGetHeatmap_MissingHistoryMeansNeutralColor(t *testing.T) {
	st, md := fixture()
	delete(md.hist, "eth|USD")

	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.NoError(t, err)
	assert.Zero(t, resp.Msg.Nodes[1].ColorValue)
}

func TestGetHeatmap_OwnershipEnforced(t *testing.T) {
	st, md := fixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	_, err := h.GetHeatmap(userCtx("intruder"), heatmapRequest())
	require.Error(t, err)
	// EnsureOwner reports NotFound so foreign IDs look like missing ones.
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetHeatmap_Validation(t *testing.T) {
	st, md := fixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)
	ctx := userCtx("u1")

	tests := []struct {
		name string
		mut  func(*apiv1.GetHeatmapRequest)
		code connect.Code
	}{
		{"scope required", func(m *apiv1.GetHeatmapRequest) {
			m.Scope = apiv1.HeatmapScope_HEATMAP_SCOPE_UNSPECIFIED
		}, connect.CodeInvalidArgument},
		{"basket scope not implemented", func(m *apiv1.GetHeatmapRequest) {
			m.Scope = apiv1.HeatmapScope_HEATMAP_SCOPE_BASKET
		}, connect.CodeUnimplemented},
		{"scope_id required", func(m *apiv1.GetHeatmapRequest) {
			m.ScopeId = ""
		}, connect.CodeInvalidArgument},
		{"market cap size metric rejected", func(m *apiv1.GetHeatmapRequest) {
			m.SizeMetric = apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_MARKET_CAP
		}, connect.CodeInvalidArgument},
		{"pnl color metric not implemented", func(m *apiv1.GetHeatmapRequest) {
			m.ColorMetric = apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_PNL_PCT
		}, connect.CodeUnimplemented},
		{"sector grouping not implemented", func(m *apiv1.GetHeatmapRequest) {
			m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_SECTOR
		}, connect.CodeUnimplemented},
		{"unknown portfolio", func(m *apiv1.GetHeatmapRequest) {
			m.ScopeId = "nope"
		}, connect.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.GetHeatmap(ctx, heatmapRequest(tt.mut))
			require.Error(t, err)
			assert.Equal(t, tt.code, connect.CodeOf(err))
		})
	}
}

// balanceFixture extends fixture with a second portfolio (extra 1 ETH on a1)
// and a portfolio-less account a3 holding 0.1 BTC (→ "unassigned" group).
func balanceFixture() (*fakeStore, *fakeMD) {
	st, md := fixture()
	st.portfolios["p2"] = &entity.Portfolio{ID: "p2", UserID: "u1", Name: "trading"}
	st.accounts["a3"] = &entity.Account{ID: "a3", UserID: "u1", Name: "cold"}
	st.holdings = append(st.holdings,
		&entity.Holding{ID: "h3", AssetID: "eth", AccountID: "a1", PortfolioID: "p2", Amount: dec("1000000000000000000"), Decimals: 18},
		&entity.Holding{ID: "h4", AssetID: "btc", AccountID: "a3", Amount: dec("10000000"), Decimals: 8},
	)
	return st, md
}

func balanceRequest(mut ...func(*apiv1.GetHeatmapRequest)) *connect.Request[apiv1.GetHeatmapRequest] {
	msg := &apiv1.GetHeatmapRequest{Scope: apiv1.HeatmapScope_HEATMAP_SCOPE_BALANCE}
	for _, m := range mut {
		m(msg)
	}
	return connect.NewRequest(msg)
}

func TestGetHeatmap_BalanceFlat(t *testing.T) {
	st, md := balanceFixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), balanceRequest())
	require.NoError(t, err)

	// Flat balance aggregates per asset across portfolios and accounts:
	// BTC 0.5 + 0.1 = 0.6 → 24000; ETH 2 + 1 = 3 → 6000.
	require.Len(t, resp.Msg.Nodes, 2)
	assert.Equal(t, "btc", resp.Msg.Nodes[0].Id)
	assert.InDelta(t, 24000, resp.Msg.Nodes[0].Size, 1e-9)
	assert.Equal(t, "eth", resp.Msg.Nodes[1].Id)
	assert.InDelta(t, 6000, resp.Msg.Nodes[1].Size, 1e-9)
}

func TestGetHeatmap_BalanceGroupByPortfolio(t *testing.T) {
	st, md := balanceFixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), balanceRequest(func(m *apiv1.GetHeatmapRequest) {
		m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_PORTFOLIO
	}))
	require.NoError(t, err)

	// Groups sorted by size desc: p1 (20000+4000), unassigned (4000), p2 (2000).
	// 3 groups + 4 leaves.
	require.Len(t, resp.Msg.Nodes, 7)

	g1, g2, g3 := resp.Msg.Nodes[0], resp.Msg.Nodes[1], resp.Msg.Nodes[2]
	assert.Equal(t, "p1", g1.Id)
	assert.Equal(t, "main", g1.Label)
	assert.InDelta(t, 24000, g1.Size, 1e-9)
	assert.Equal(t, "unassigned", g2.Id)
	assert.Equal(t, "Unassigned", g2.Label)
	assert.InDelta(t, 4000, g2.Size, 1e-9)
	assert.Equal(t, "p2", g3.Id)
	assert.Equal(t, "trading", g3.Label)
	assert.InDelta(t, 2000, g3.Size, 1e-9)

	leaf := resp.Msg.Nodes[3]
	assert.Equal(t, "p1:btc", leaf.Id)
	assert.Equal(t, "p1", leaf.ParentId)
}

func TestGetHeatmap_BalanceGroupByAccount(t *testing.T) {
	st, md := balanceFixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), balanceRequest(func(m *apiv1.GetHeatmapRequest) {
		m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT
	}))
	require.NoError(t, err)

	// a1 holds 3 ETH total (two portfolios merge within the account leaf).
	require.Len(t, resp.Msg.Nodes, 6) // 3 groups + 3 leaves
	assert.Equal(t, "a2", resp.Msg.Nodes[0].Id) // 20000
	assert.Equal(t, "a1", resp.Msg.Nodes[1].Id) // 6000
	assert.Equal(t, "a3", resp.Msg.Nodes[2].Id) // 4000
}

func TestGetHeatmap_BalanceScopesToCaller(t *testing.T) {
	st, md := balanceFixture()
	st.portfolios["px"] = &entity.Portfolio{ID: "px", UserID: "other", Name: "foreign"}
	st.holdings = append(st.holdings,
		&entity.Holding{ID: "hx", AssetID: "btc", AccountID: "ax", PortfolioID: "px", Amount: dec("100000000"), Decimals: 8},
	)
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	resp, err := h.GetHeatmap(userCtx("u1"), balanceRequest())
	require.NoError(t, err)

	// Foreign holding (1 BTC = 40000) must not leak into u1's balance.
	assert.InDelta(t, 24000, resp.Msg.Nodes[0].Size, 1e-9)
}

func TestGetHeatmap_BalanceValidation(t *testing.T) {
	st, md := balanceFixture()
	h := NewHandler(st, testLogger()).WithMarketDataClient(md)

	_, err := h.GetHeatmap(userCtx("u1"), balanceRequest(func(m *apiv1.GetHeatmapRequest) {
		m.ScopeId = "p1"
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = h.GetHeatmap(context.Background(), balanceRequest())
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// Portfolio grouping is balance-only.
	_, err = h.GetHeatmap(userCtx("u1"), heatmapRequest(func(m *apiv1.GetHeatmapRequest) {
		m.GroupBy = apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_PORTFOLIO
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetHeatmap_NoMarketDataClient(t *testing.T) {
	st, _ := fixture()
	h := NewHandler(st, testLogger())

	_, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestWindowDuration(t *testing.T) {
	assert.Equal(t, 24*time.Hour, windowDuration(apiv1.HeatmapWindow_HEATMAP_WINDOW_UNSPECIFIED))
	assert.Equal(t, 24*time.Hour, windowDuration(apiv1.HeatmapWindow_HEATMAP_WINDOW_24H))
	assert.Equal(t, 7*24*time.Hour, windowDuration(apiv1.HeatmapWindow_HEATMAP_WINDOW_7D))
	assert.Equal(t, 30*24*time.Hour, windowDuration(apiv1.HeatmapWindow_HEATMAP_WINDOW_30D))
}
