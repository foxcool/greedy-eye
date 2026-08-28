package portfolio

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// unpricedFixture puts one holding of one asset in the portfolio and answers
// every price lookup with NotFound, which is the state of the nine delisted
// FinEx papers on prod: an asset in the catalogue with no price row anywhere.
func unpricedFixture(t *testing.T) (*mockStore, *mockMDClient) {
	t.Helper()
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: time.Now()},
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).
		Return(nil, connect.NewError(connect.CodeNotFound, assert.AnError))
	md.On("GetAsset", mock.Anything, assetReq("USD")).
		Return(connect.NewResponse(&apiv1.Asset{Id: "USD", Symbol: strPtr("USD")}), nil).Maybe()
	md.On("GetAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.Asset{Id: testAssetID, Symbol: strPtr("FXGD")}), nil)
	return s, md
}

func pricingStatus(md *mockMDClient, st *apiv1.AssetPricingStatus) {
	resp := &apiv1.GetPricingStatusResponse{}
	if st != nil {
		resp.Statuses = []*apiv1.AssetPricingStatus{st}
	}
	md.On("GetPricingStatus", mock.Anything, mock.Anything).Return(connect.NewResponse(resp), nil)
}

func firstUnpriced(t *testing.T, s *mockStore, md *mockMDClient) *apiv1.UnpricedHolding {
	t.Helper()
	resp, err := newHandler(s).WithMarketDataClient(md).CalculatePortfolioValue(
		ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{PortfolioId: testPortfolioID, QuoteAssetId: "usd"}),
	)
	require.NoError(t, err)
	require.Len(t, resp.Msg.Coverage.GetUnpriced(), 1)
	return resp.Msg.Coverage.GetUnpriced()[0]
}

// The prod case: every source has been asked for weeks and none has ever
// returned a price. That is a different statement from "we have not looked".
func TestUnpriced_SourcesExhaustedReadsAsNeverPriced(t *testing.T) {
	asked := time.Now().Add(-11 * 24 * time.Hour)

	s, md := unpricedFixture(t)
	pricingStatus(md, &apiv1.AssetPricingStatus{
		AssetId:      testAssetID,
		EverPriced:   false,
		FirstAskedAt: timestamppb.New(asked),
		LastAskedAt:  timestamppb.New(time.Now()),
		SourcesAsked: 4,
	})

	item := firstUnpriced(t, s, md)

	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NEVER_PRICED, item.GetReason())
	require.NotNil(t, item.GetAskedSince())
	assert.WithinDuration(t, asked, item.GetAskedSince().AsTime(), time.Second,
		"how long the silence has lasted is what makes the reason actionable")
}

// No attempt record at all means nothing has looked yet. Calling that
// 'never priced' would blame the instrument for our own pipeline.
func TestUnpriced_NoAttemptRecordStaysNoQuote(t *testing.T) {
	s, md := unpricedFixture(t)
	pricingStatus(md, nil)

	item := firstUnpriced(t, s, md)

	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE, item.GetReason())
	assert.Nil(t, item.GetAskedSince())
}

// An asset some source priced once has a price row; failing to value it now is
// about the path to the quote asset, not about absence of any quote at all.
func TestUnpriced_PricedInThePastStaysNoQuote(t *testing.T) {
	s, md := unpricedFixture(t)
	pricingStatus(md, &apiv1.AssetPricingStatus{
		AssetId:      testAssetID,
		EverPriced:   true,
		FirstAskedAt: timestamppb.New(time.Now().Add(-30 * 24 * time.Hour)),
		SourcesAsked: 2,
	})

	item := firstUnpriced(t, s, md)

	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE, item.GetReason())
}

// Losing the attempt log costs detail, never the valuation: the reason falls
// back to what the caller would have reported without it.
func TestUnpriced_StatusFailureDegradesToNoQuote(t *testing.T) {
	s, md := unpricedFixture(t)
	md.On("GetPricingStatus", mock.Anything, mock.Anything).
		Return(nil, connect.NewError(connect.CodeUnavailable, assert.AnError))

	item := firstUnpriced(t, s, md)

	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE, item.GetReason())
	assert.Nil(t, item.GetAskedSince())
}

// A thin market is a judgement about the quote that exists; the attempt log has
// nothing to say about it and must not overwrite it.
func TestUnpriced_ThinMarketIsNotReinterpreted(t *testing.T) {
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: time.Now()},
	}, "", nil)

	thinVolume := "1000000000000" // $10k a day, under the ADR-009 floor
	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Price{
		AssetId: testAssetID, BaseAssetId: "usd", Last: "100", Decimals: 8, Volume: &thinVolume,
		Timestamp: timestamppb.New(time.Now()),
	}), nil)
	md.On("GetAsset", mock.Anything, assetReq("USD")).
		Return(connect.NewResponse(&apiv1.Asset{Id: "USD", Symbol: strPtr("USD")}), nil).Maybe()
	md.On("GetAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.Asset{Id: testAssetID, Symbol: strPtr("MNEP")}), nil)
	pricingStatus(md, &apiv1.AssetPricingStatus{
		AssetId: testAssetID, EverPriced: false,
		FirstAskedAt: timestamppb.New(time.Now().Add(-time.Hour)), SourcesAsked: 3,
	})

	item := firstUnpriced(t, s, md)

	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET, item.GetReason())
	assert.Nil(t, item.GetAskedSince())
}
