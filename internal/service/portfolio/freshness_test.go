package portfolio

import (
	"context"
	"errors"
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
	"github.com/foxcool/greedy-eye/internal/pricefresh"
)

// fakeSettings answers the one call a valuation makes. A nil value means the
// key was never written, which is the normal state of a fresh instance.
type fakeSettings struct {
	value string
	err   error
}

func (f *fakeSettings) GetSetting(_ context.Context, _ *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.value == "" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("setting not found"))
	}
	return connect.NewResponse(&apiv1.GetSettingResponse{
		Setting: &apiv1.Setting{Key: pricefresh.SettingKey, Value: f.value},
	}), nil
}

// pricedAt builds a quote of 100 units observed at t.
func pricedAt(t time.Time) *apiv1.Price {
	p := &apiv1.Price{AssetId: testAssetID, BaseAssetId: "usd", Last: "100", Decimals: 0}
	if !t.IsZero() {
		p.Timestamp = timestamppb.New(t)
	}
	return p
}

// twoHoldings puts two positions of one unit each in the portfolio, so a test
// can price them separately by asset.
func twoHoldings(assetA, assetB string) []*entity.Holding {
	now := time.Now()
	return []*entity.Holding{
		{ID: "h1", AssetID: assetA, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: now},
		{ID: "h2", AssetID: assetB, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: now},
	}
}

func priceFor(md *mockMDClient, assetID string, at time.Time) {
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == assetID
	})).Return(connect.NewResponse(pricedAt(at)), nil)
}

func valuation(t *testing.T, h *Handler) *apiv1.PortfolioValueResponse {
	t.Helper()
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId:  testPortfolioID,
		QuoteAssetId: "usd",
	}))
	require.NoError(t, err)
	return resp.Msg
}

// A stale quote is named, not removed. Dropping it would take the position out
// of the total on every provider outage and put it back on recovery — a total
// that moves for reasons that have nothing to do with the market.
func TestCalculatePortfolioValue_StaleQuoteStaysInTheTotalAndIsCounted(t *testing.T) {
	const otherAsset = "01926d35-6a1e-7005-8005-000000000002"
	stale := time.Now().Add(-67 * 24 * time.Hour) // the FXUS case: last OTC print, two months back
	fresh := time.Now().Add(-30 * time.Minute)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(twoHoldings(testAssetID, otherAsset), "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, stale)
	priceFor(md, otherAsset, fresh)

	msg := valuation(t, newHandler(s).WithMarketDataClient(md))

	assert.Equal(t, "20000", msg.TotalValueAmount,
		"both positions are still worth 100 each: staleness labels the total, it does not shrink it")
	assert.Equal(t, uint32(2), msg.Coverage.GetPricedCount())
	assert.Equal(t, uint32(1), msg.Coverage.GetStaleCount())
	require.NotNil(t, msg.Coverage.GetPricesAsOf())
	assert.WithinDuration(t, fresh, msg.Coverage.GetPricesAsOf().AsTime(), time.Second,
		"the oldest FRESH quote dates the total: a quote nothing refreshes any more would pin "+
			"this field on the one position that stopped being covered")
}

// The two fields answer different questions and must not be read off one number:
// StaleCount says how much of the total is doubtful, PricesAsOf says how current
// the part that is still covered is. Measured on prod 2026-08-27: after the sweep
// schedule thawed, prices_as_of still read 2026-08-11 because one position whose
// source had quietly stopped quoting it dated the whole portfolio.
func TestCalculatePortfolioValue_StaleQuoteDoesNotDateTheTotal(t *testing.T) {
	const otherAsset = "01926d35-6a1e-7005-8005-000000000002"
	stale := time.Now().Add(-67 * 24 * time.Hour)
	fresh := time.Now().Add(-30 * time.Minute)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(twoHoldings(testAssetID, otherAsset), "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, stale)
	priceFor(md, otherAsset, fresh)

	msg := valuation(t, newHandler(s).WithMarketDataClient(md))

	require.NotNil(t, msg.Coverage.GetPricesAsOf())
	assert.True(t, msg.Coverage.GetPricesAsOf().AsTime().After(stale.Add(time.Hour)),
		"the stale quote must not date the total")
	assert.Equal(t, uint32(1), msg.Coverage.GetStaleCount(),
		"and it must still be disclosed — dating and disclosure are separate")
}

// Every quote stale: the field goes absent rather than reporting a date nothing
// stands behind. Absent already means "no priced holding to date" downstream, and
// a portfolio whose every source went quiet is that case, not a fresh one.
func TestCalculatePortfolioValue_AllQuotesStaleLeavesPricesAsOfUnset(t *testing.T) {
	const otherAsset = "01926d35-6a1e-7005-8005-000000000002"
	stale := time.Now().Add(-67 * 24 * time.Hour)
	older := time.Now().Add(-90 * 24 * time.Hour)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(twoHoldings(testAssetID, otherAsset), "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, stale)
	priceFor(md, otherAsset, older)

	msg := valuation(t, newHandler(s).WithMarketDataClient(md))

	assert.Nil(t, msg.Coverage.GetPricesAsOf(),
		"no fresh quote behind the total, so there is no date to give")
	assert.Equal(t, uint32(2), msg.Coverage.GetStaleCount(),
		"both are still in the total and both are still named")
}

func TestCalculatePortfolioValue_FreshQuotesCountNoStaleness(t *testing.T) {
	const otherAsset = "01926d35-6a1e-7005-8005-000000000002"
	// Just inside the default: a rouble rate published before a long weekend
	// must not read as a dead market.
	recent := time.Now().Add(-47 * time.Hour)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(twoHoldings(testAssetID, otherAsset), "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, recent)
	priceFor(md, otherAsset, time.Now())

	msg := valuation(t, newHandler(s).WithMarketDataClient(md))

	assert.Zero(t, msg.Coverage.GetStaleCount())
	assert.Equal(t, uint32(2), msg.Coverage.GetPricedCount())
}

// The bead's own case: a portfolio priced entirely by fossils. The number still
// comes out, and every position in it is declared questionable.
func TestCalculatePortfolioValue_WhollyStalePortfolio(t *testing.T) {
	const otherAsset = "01926d35-6a1e-7005-8005-000000000002"
	longDead := time.Now().Add(-3 * 365 * 24 * time.Hour) // FXRU: last data 2023-08-08

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(twoHoldings(testAssetID, otherAsset), "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, longDead)
	priceFor(md, otherAsset, longDead.Add(time.Hour))

	msg := valuation(t, newHandler(s).WithMarketDataClient(md))

	assert.Equal(t, "20000", msg.TotalValueAmount)
	assert.Equal(t, msg.Coverage.GetPricedCount(), msg.Coverage.GetStaleCount(),
		"every priced position rests on a fossil, and the block says so")
}

// The setting is the point of the whole design: an instance that sweeps often
// may demand fresher quotes than the built-in default allows.
func TestCalculatePortfolioValue_SettingTightensTheThreshold(t *testing.T) {
	twoHoursOld := time.Now().Add(-2 * time.Hour)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: time.Now()},
	}, "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, twoHoursOld)

	base := newHandler(s).WithMarketDataClient(md)

	t.Run("default leaves a two-hour-old quote current", func(t *testing.T) {
		assert.Zero(t, valuation(t, base).Coverage.GetStaleCount())
	})

	t.Run("a one-hour policy makes it stale", func(t *testing.T) {
		h := base.WithSettingsClient(&fakeSettings{value: `{"price_max_age":"1h"}`})
		assert.Equal(t, uint32(1), valuation(t, h).Coverage.GetStaleCount())
	})

	t.Run("an unwritten setting falls back to the default", func(t *testing.T) {
		h := base.WithSettingsClient(&fakeSettings{})
		assert.Zero(t, valuation(t, h).Coverage.GetStaleCount())
	})

	t.Run("a malformed setting falls back to the default rather than to nothing", func(t *testing.T) {
		h := base.WithSettingsClient(&fakeSettings{value: `{"price_max_age":"whenever"}`})
		assert.Zero(t, valuation(t, h).Coverage.GetStaleCount())
	})
}

// prices.timestamp is NOT NULL, so a quote without one is a path that failed to
// populate it. Dating the total from it would invent an observation.
func TestCalculatePortfolioValue_QuoteWithoutATimeIsNotDated(t *testing.T) {
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: time.Now()},
	}, "", nil)

	md := &mockMDClient{}
	priceFor(md, testAssetID, time.Time{})

	msg := valuation(t, newHandler(s).WithMarketDataClient(md))

	assert.Zero(t, msg.Coverage.GetStaleCount(), "an absent timestamp is not an old quote")
	assert.Nil(t, msg.Coverage.GetPricesAsOf())
	assert.Equal(t, "10000", msg.TotalValueAmount)
}
