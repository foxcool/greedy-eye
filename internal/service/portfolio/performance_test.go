package portfolio

import (
	"math"
	"testing"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// A holding whose decimals sum past int32 must not be valued with a wrapped
// divisor: it is left out, and the honest holding alone decides the return.
func TestGetPortfolioPerformance_OverflowingDecimalsAreSkipped(t *testing.T) {
	good := &entity.Holding{ID: "h-good", AssetID: "asset-good", Amount: decimal.NewFromInt(1000)}
	// MaxInt32+MaxInt32 wraps to -2 in int32, turning the divisor into 10^-2.
	absurd := &entity.Holding{ID: "h-absurd", AssetID: "asset-absurd", Amount: decimal.NewFromInt(1), Decimals: math.MaxUint32}

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{good, absurd}, "", nil)

	md := &mockMDClient{}
	latest := func(asset string) any {
		return mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool { return r.Msg.AssetId == asset })
	}
	history := func(asset string) any {
		return mock.MatchedBy(func(r *connect.Request[apiv1.ListPriceHistoryRequest]) bool { return r.Msg.AssetId == asset })
	}
	md.On("GetLatestPrice", mock.Anything, latest("asset-good")).
		Return(connect.NewResponse(&apiv1.Price{Last: "110"}), nil)
	md.On("ListPriceHistory", mock.Anything, history("asset-good")).
		Return(connect.NewResponse(&apiv1.ListPriceHistoryResponse{Prices: []*apiv1.Price{{Last: "100"}}}), nil)
	md.On("GetLatestPrice", mock.Anything, latest("asset-absurd")).
		Return(connect.NewResponse(&apiv1.Price{Last: "1", Decimals: math.MaxUint32}), nil)
	md.On("ListPriceHistory", mock.Anything, history("asset-absurd")).
		Return(connect.NewResponse(&apiv1.ListPriceHistoryResponse{
			Prices: []*apiv1.Price{{Last: "1", Decimals: math.MaxUint32}},
		}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.GetPortfolioPerformance(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetPortfolioPerformanceRequest{PortfolioId: testPortfolioID}))
	require.NoError(t, err)
	assert.InDelta(t, 10.0, resp.Msg.ReturnPercentage, 1e-9)
}

func TestDivisorScale(t *testing.T) {
	cases := []struct {
		name string
		a, b uint32
		want int32
		ok   bool
	}{
		{"typical", 8, 6, 14, true},
		{"zero", 0, 0, 0, true},
		{"at the cap", maxDivisorScale, 0, maxDivisorScale, true},
		{"over the cap", maxDivisorScale, 1, 0, false},
		{"int32 operands that wrap when added", math.MaxInt32, math.MaxInt32, 0, false},
		{"uint32 operands past int32", math.MaxUint32, math.MaxUint32, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := divisorScale(c.a, c.b)
			assert.Equal(t, c.ok, ok)
			if ok {
				assert.Equal(t, c.want, got)
			}
		})
	}
}

// An out-of-range historical scale falls back to the current value, the same
// as a missing history row: 0% for that holding, never a wrapped or stalled
// baseline. The honest holding beside it makes the result tell the fallback
// from a skip. On the old code this case stalls in Div instead of asserting.
func TestGetPortfolioPerformance_OverflowingHistoryDecimalsFallBack(t *testing.T) {
	good := &entity.Holding{ID: "h-good", AssetID: "asset-good", Amount: decimal.NewFromInt(1000)}
	bad := &entity.Holding{ID: "h-bad", AssetID: "asset-bad", Amount: decimal.NewFromInt(1000)}
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{good, bad}, "", nil)

	md := &mockMDClient{}
	latest := func(asset string) any {
		return mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool { return r.Msg.AssetId == asset })
	}
	history := func(asset string) any {
		return mock.MatchedBy(func(r *connect.Request[apiv1.ListPriceHistoryRequest]) bool { return r.Msg.AssetId == asset })
	}
	md.On("GetLatestPrice", mock.Anything, latest("asset-good")).Return(connect.NewResponse(&apiv1.Price{Last: "110"}), nil)
	md.On("ListPriceHistory", mock.Anything, history("asset-good")).Return(connect.NewResponse(&apiv1.ListPriceHistoryResponse{
		Prices: []*apiv1.Price{{Last: "100"}},
	}), nil)
	md.On("GetLatestPrice", mock.Anything, latest("asset-bad")).Return(connect.NewResponse(&apiv1.Price{Last: "110"}), nil)
	md.On("ListPriceHistory", mock.Anything, history("asset-bad")).Return(connect.NewResponse(&apiv1.ListPriceHistoryResponse{
		Prices: []*apiv1.Price{{Last: "100", Decimals: math.MaxUint32}},
	}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.GetPortfolioPerformance(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetPortfolioPerformanceRequest{PortfolioId: testPortfolioID}))
	require.NoError(t, err)
	// current 220000, baseline 100000 + 110000 (fallback): 10000/210000.
	assert.InDelta(t, 100.0*10000/210000, resp.Msg.ReturnPercentage, 1e-9)
}
