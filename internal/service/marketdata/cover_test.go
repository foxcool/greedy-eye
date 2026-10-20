package marketdata

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
	"github.com/foxcool/greedy-eye/internal/store"
)

const (
	coverUSD        = "00000000-0000-0000-0000-0000000000d0"
	coverUSDT       = "00000000-0000-0000-0000-0000000000d7"
	coverDeep       = "00000000-0000-0000-0000-00000000000a"
	coverThin       = "00000000-0000-0000-0000-00000000000b"
	coverUSDTDeep   = "00000000-0000-0000-0000-00000000000c"
	coverNoRate     = "00000000-0000-0000-0000-00000000000e"
	coverNoRateBase = "00000000-0000-0000-0000-0000000000ee"

	coverBudget = 60
)

// quoteWithVolume is a traded quote with turnover in its own base, at 0 decimals.
func quoteWithVolume(assetID, baseID string, volume int64) *entity.StoredPrice {
	return &entity.StoredPrice{
		AssetID: assetID, SourceID: "binance", BaseAssetID: baseID,
		Last:       decimal.NewFromInt(1),
		Volume:     decimal.NewNullDecimal(decimal.NewFromInt(volume)),
		Provenance: entity.PriceProvenanceTraded,
		Timestamp:  time.Now(),
	}
}

func coverStore(t *testing.T) *mockStore {
	t.Helper()
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("GetAssetBySymbol", mock.Anything, "USD").Return(&entity.Asset{ID: coverUSD, Symbol: "USD"}, nil)
	s.On("FreshTradedQuotes", mock.Anything, "fake", mock.Anything).Return([]*entity.StoredPrice{
		quoteWithVolume(coverDeep, coverUSD, 5_000_000),
		quoteWithVolume(coverThin, coverUSD, 40_000),
		quoteWithVolume(coverUSDTDeep, coverUSDT, 5_000_000),
		quoteWithVolume(coverNoRate, coverNoRateBase, 5_000_000),
	}, nil)
	// USDT converts to USD; the other base has no rate and so proves nothing.
	s.On("GetLatestPrice", mock.Anything, coverUSDT, coverUSD, "").Return(&entity.StoredPrice{
		AssetID: coverUSDT, BaseAssetID: coverUSD, Last: decimal.NewFromInt(1),
	}, nil)
	s.On("GetLatestPrice", mock.Anything, mock.Anything, mock.Anything, "").Return(nil, store.ErrNotFound).Maybe()
	return s
}

// TestFetchExternalPrices_MeteredSourceYieldsToDeepTradedQuotes: a plan-metered
// source skips assets another source quotes from a real market, counts them,
// and still asks once a day.
func TestFetchExternalPrices_MeteredSourceYieldsToDeepTradedQuotes(t *testing.T) {
	paid := testAsset("paid-1")
	s := coverStore(t)

	var opts StalePricingOpts
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return o.Limit == coverBudget
	})).Run(func(args mock.Arguments) { opts = args.Get(1).(StalePricingOpts) }).
		Return([]*entity.Asset{paid}, nil)
	// Reported as the store counts it, not as len(Covered).
	s.On("CountCoveredTargets", mock.Anything, mock.Anything).Return(5, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	p := &fakePriceProvider{budget: coverBudget, hasBudget: true, prices: map[string]bool{"paid-1": true}}
	h := newHandler(s).WithProvider("fake", p).WithRefreshWindow(time.Hour)

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{coverDeep, coverUSDTDeep}, opts.Covered,
		"thin and unconvertible quotes do not cover")
	assert.WithinDuration(t, time.Now().Add(dueSlack-coverRecheck), opts.CoveredRecheckBefore, time.Second)
	assert.Equal(t, map[string]int32{"fake": 5}, resp.Msg.CoveredAssets)
}

// TestFetchExternalPrices_AllCoveredIsAnIdleReason: a source whose whole due
// set is covered says so instead of reading as "nothing due".
func TestFetchExternalPrices_AllCoveredIsAnIdleReason(t *testing.T) {
	s := coverStore(t)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, nil)
	s.On("CountCoveredTargets", mock.Anything, mock.Anything).Return(3, nil)

	p := &fakePriceProvider{budget: coverBudget, hasBudget: true}
	h := newHandler(s).WithProvider("fake", p).WithRefreshWindow(time.Hour)

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "covered", resp.Msg.IdleSources["fake"])
	assert.Equal(t, int32(3), resp.Msg.CoveredAssets["fake"])
}

// TestFetchExternalPrices_UnmeteredSourceDoesNotYield: without a plan budget
// nothing is scarce, so nothing is looked up or skipped.
func TestFetchExternalPrices_UnmeteredSourceDoesNotYield(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return len(o.Covered) == 0
	})).Return([]*entity.Asset{}, nil)
	s.On("SweepSchedule", mock.Anything, mock.Anything).Return([]*entity.SourceSchedule{}, nil).Maybe()

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{}).WithRefreshWindow(time.Hour)
	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	s.AssertNotCalled(t, "FreshTradedQuotes", mock.Anything, mock.Anything, mock.Anything)
}
