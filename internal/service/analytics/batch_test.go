package analytics

import (
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingMD counts every market-data call the heatmap makes. In a split
// deployment each one is a network hop, so the count is the cost.
type countingMD struct {
	inner *fakeMD
	calls int
}

func (c *countingMD) GetAsset(ctx context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	c.calls++
	return c.inner.GetAsset(ctx, req)
}

func (c *countingMD) ListAssets(ctx context.Context, req *connect.Request[apiv1.ListAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error) {
	c.calls++
	return c.inner.ListAssets(ctx, req)
}

func (c *countingMD) GetPrices(ctx context.Context, req *connect.Request[apiv1.GetPricesRequest]) (*connect.Response[apiv1.GetPricesResponse], error) {
	c.calls++
	return c.inner.GetPrices(ctx, req)
}

func (c *countingMD) GetPricingStatus(ctx context.Context, req *connect.Request[apiv1.GetPricingStatusRequest]) (*connect.Response[apiv1.GetPricingStatusResponse], error) {
	c.calls++
	return c.inner.GetPricingStatus(ctx, req)
}

// scaledFixture builds a portfolio of n assets walking every pricing path:
// quoted directly in USD, quoted only in BTC and crossed through BTC/USD, and
// not quoted at all.
func scaledFixture(n int) (*fakeStore, *fakeMD) {
	st, md := fixture()
	st.holdings = nil
	for i := range n {
		id := fmt.Sprintf("asset-%d", i)
		st.holdings = append(st.holdings, &entity.Holding{
			ID: "h-" + id, AssetID: id, AccountID: "a1", PortfolioID: "p1", Amount: dec("100"), Decimals: 0,
		})
		md.assets[id] = &apiv1.Asset{Id: id, Name: id, Symbol: strPtr(fmt.Sprintf("A%d", i))}
		switch i % 3 {
		case 0:
			md.latest[id+"|"] = price(id, "USD", "1000", 2)
			md.latest[id+"|USD"] = price(id, "USD", "1000", 2)
			md.hist[id+"|USD"] = price(id, "USD", "900", 2)
		case 1:
			md.latest[id+"|"] = price(id, "btc", "100", 8)
			md.hist[id+"|btc"] = price(id, "btc", "90", 8)
		}
	}
	return st, md
}

// The heatmap reads market data once per set, not once per holding: the
// number of calls must not grow with the portfolio.
func TestGetHeatmap_MarketDataCallsDoNotGrowWithHoldings(t *testing.T) {
	calls := func(n int) int {
		st, md := scaledFixture(n)
		counter := &countingMD{inner: md}
		h := NewHandler(st.client(), testLogger()).WithMarketDataClient(counter)
		resp, err := h.GetHeatmap(userCtx("u1"), heatmapRequest())
		require.NoError(t, err)
		require.Equal(t, uint32(n-n/3), resp.Msg.Coverage.PricedCount)
		return counter.calls
	}
	small, large := calls(3), calls(60)
	assert.Equal(t, small, large, "market-data calls for 3 holdings vs 60")
}

// A pair priceAssets did not collect must fail, not read as "no price": the
// latter would drop a priced asset off the map without a trace.
func TestPrefetchedPrices_UncollectedPairFails(t *testing.T) {
	p := prefetchedPrices{{asset: "eth", base: "USD"}: nil}

	_, err := p.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{AssetId: "eth", BaseAssetId: "USD"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "collected, no price")

	_, err = p.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{AssetId: "eth"}))
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err), "never collected")
}
