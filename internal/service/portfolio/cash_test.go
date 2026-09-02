package portfolio

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// TestCalculatePortfolioValue_CashInTheDisplayCurrency: cash counts at face
// value in the currency the total is expressed in.
//
// Latent until the first broker sync brought cash in: a wallet holds tokens and
// an exchange holds crypto balances, so the display currency was never a
// position. When it became one, both USD rows left a USD total reported as
// NEVER_PRICED — half a portfolio missing, explained by a missing price source
// that cannot exist (personal-v2a1).
func TestCalculatePortfolioValue_CashInTheDisplayCurrency(t *testing.T) {
	const (
		usd   = "019e88f7-3279-7798-9eab-cb5614c73385"
		other = "01926d35-6a1e-7005-8005-000000000002"
	)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		// $250.00 of cash, and one unit of something quoted at 100.
		{ID: "h1", AssetID: usd, Amount: decimal.NewFromInt(25000), Decimals: 2, UpdatedAt: time.Now()},
		{ID: "h2", AssetID: other, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: time.Now()},
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == other
	})).Return(connect.NewResponse(&apiv1.Price{AssetId: other, BaseAssetId: usd, Last: "100", Decimals: 0}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(
		&apiv1.CalculatePortfolioValueRequest{PortfolioId: testPortfolioID, QuoteAssetId: usd}))
	require.NoError(t, err)

	assert.Equal(t, "35000", resp.Msg.TotalValueAmount, "250 of cash + 100 of the other position")
	assert.Equal(t, uint32(2), resp.Msg.Coverage.PricedCount)
	assert.Zero(t, resp.Msg.Coverage.UnpricedCount, "the display currency is not a coverage gap")
	assert.Empty(t, resp.Msg.Coverage.Unpriced)
	assert.Zero(t, resp.Msg.Coverage.StaleCount, "an identity price cannot go stale")

	for _, c := range md.Calls {
		if c.Method != "GetLatestPrice" {
			continue
		}
		req := c.Arguments.Get(1).(*connect.Request[apiv1.GetLatestPriceRequest])
		assert.NotEqual(t, usd, req.Msg.AssetId, "there is no source to ask what a dollar is worth in dollars")
	}
}

// TestCalculatePortfolioValue_CashInAnotherCurrencyIsStillDisclosed: only the
// display currency is free. Cash in a currency with no rate stays out of the
// total and is reported, exactly as before.
func TestCalculatePortfolioValue_CashInAnotherCurrencyIsStillDisclosed(t *testing.T) {
	const (
		usd = "019e88f7-3279-7798-9eab-cb5614c73385"
		rub = "019fcd3e-220a-7e0e-a60c-1f8e2bc45f25"
	)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: rub, Amount: decimal.NewFromInt(10000), Decimals: 2, UpdatedAt: time.Now()},
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).
		Return(nil, connect.NewError(connect.CodeNotFound, assert.AnError))
	md.On("GetPricingStatus", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.GetPricingStatusResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(
		&apiv1.CalculatePortfolioValueRequest{PortfolioId: testPortfolioID, QuoteAssetId: usd}))
	require.NoError(t, err)

	assert.Equal(t, "0", resp.Msg.TotalValueAmount)
	assert.Equal(t, uint32(1), resp.Msg.Coverage.UnpricedCount, "roubles in a dollar total still need a rate")
}
