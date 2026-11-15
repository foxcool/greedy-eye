package portfolio

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// countingMD answers prices from a map keyed "asset|base" ("asset|" is the
// latest in any base) and counts every market-data call. In a split deployment
// each call is a network hop, so the count is the cost. The embedded client is
// nil: a call the valuation is not expected to make panics.
type countingMD struct {
	MarketDataClient
	latest map[string]*apiv1.Price
	calls  int
}

func (c *countingMD) GetAsset(_ context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	c.calls++
	return connect.NewResponse(&apiv1.Asset{Id: req.Msg.Id}), nil
}

func (c *countingMD) GetLatestPrice(_ context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	c.calls++
	if p, ok := c.latest[req.Msg.AssetId+"|"+req.Msg.BaseAssetId]; ok {
		return connect.NewResponse(p), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no price"))
}

func (c *countingMD) GetPrices(_ context.Context, req *connect.Request[apiv1.GetPricesRequest]) (*connect.Response[apiv1.GetPricesResponse], error) {
	c.calls++
	resp := &apiv1.GetPricesResponse{}
	for _, l := range req.Msg.GetLookups() {
		resp.Results = append(resp.Results, &apiv1.PriceLookupResult{Lookup: l, Price: c.latest[l.AssetId+"|"+l.BaseAssetId]})
	}
	return connect.NewResponse(resp), nil
}

func (c *countingMD) GetPricingStatus(_ context.Context, _ *connect.Request[apiv1.GetPricingStatusRequest]) (*connect.Response[apiv1.GetPricingStatusResponse], error) {
	c.calls++
	return connect.NewResponse(&apiv1.GetPricingStatusResponse{}), nil
}

// The total reads market data once per set, not once per holding: the number
// of calls must not grow with the portfolio. Every asset is priced, half
// directly in USD and half through BTC/USD, so both pricing paths are walked.
func TestCalculatePortfolioValue_MarketDataCallsDoNotGrowWithHoldings(t *testing.T) {
	calls := func(n int) int {
		now := timestamppb.New(time.Now())
		md := &countingMD{latest: map[string]*apiv1.Price{
			"BTC|USD": {AssetId: "BTC", BaseAssetId: "USD", Last: "6000000", Decimals: 2, Timestamp: now},
		}}
		var holdings []*entity.Holding
		for i := range n {
			id := fmt.Sprintf("asset-%d", i)
			holdings = append(holdings, &entity.Holding{
				ID: "h-" + id, AssetID: id, PortfolioID: testPortfolioID, Amount: decimal.NewFromInt(1), UpdatedAt: time.Now(),
			})
			p := &apiv1.Price{AssetId: id, BaseAssetId: "USD", Last: "100", Decimals: 0, Timestamp: now}
			if i%2 == 1 {
				p = &apiv1.Price{AssetId: id, BaseAssetId: "BTC", Last: "1", Decimals: 3, Timestamp: now}
			} else {
				md.latest[id+"|USD"] = p
			}
			md.latest[id+"|"] = p
		}
		s := &mockStore{}
		s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
		s.On("ListHoldings", mock.Anything, mock.Anything).Return(holdings, "", nil)

		h := newHandler(s).WithMarketDataClient(md)
		resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(
			&apiv1.CalculatePortfolioValueRequest{PortfolioId: testPortfolioID, QuoteAssetId: "USD"}))
		require.NoError(t, err)
		require.Equal(t, uint32(n), resp.Msg.Coverage.PricedCount)
		// 100 USD per even asset, 0.001 BTC = 60 USD per odd one, in cents.
		want := decimal.NewFromInt(int64((n+1)/2*100+n/2*60) * 100)
		require.Equal(t, want.String(), resp.Msg.TotalValueAmount)
		return md.calls
	}
	small, large := calls(3), calls(60)
	assert.Equal(t, small, large, "market-data calls for 3 holdings vs 60")
}
