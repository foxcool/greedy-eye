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

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// sweepAccount builds a wallet account owned by userID, as the sweep sees it:
// selected without a request context, so its owner is whatever the row says.
func sweepAccount(id, userID, name string) *entity.Account {
	return &entity.Account{
		ID:     id,
		UserID: userID,
		Name:   name,
		Type:   entity.AccountTypeWallet,
		Data:   map[string]string{"address": "0x" + id, "chain": "eth"},
	}
}

// TestSyncDueAccounts_SyncsUnderTheOwnersIdentity: nothing re-reads balances on a
// schedule, and a user-agnostic sweep cannot: SyncAccount resolves wallet
// syncers and exchange credentials per user, so an unattributed run would reach
// only what an admin shared system-wide (personal-cpw). Each account therefore
// syncs as its own owner — and an account belonging to someone else must not
// leak into that run.
func TestSyncDueAccounts_SyncsUnderTheOwnersIdentity(t *testing.T) {
	first := sweepAccount("11111111-1111-1111-1111-111111111111", "user-a", "hot")
	second := sweepAccount("22222222-2222-2222-2222-222222222222", "user-b", "main")

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{first, second}, nil).Once()
	s.On("GetAccount", mock.Anything, first.ID).Return(first, nil)
	s.On("GetAccount", mock.Anything, second.ID).Return(second, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: "h1"}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0x"+first.ID, []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "DAI", Amount: "100", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
	}, nil)
	ws.On("SyncWallet", mock.Anything, "0x"+second.ID, []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "WETH", Amount: "100", Decimals: 18, ContractAddress: "0xweth", Chain: "eth"},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{})
	require.NoError(t, err)
	assert.Equal(t, 2, report.Due)
	assert.Equal(t, 2, report.Synced)
	assert.Zero(t, report.Failed)
	assert.Equal(t, int32(2), report.HoldingsUpserted)
	s.AssertExpectations(t)
}

// TestSyncDueAccounts_StalenessCutoffAndBudget: selection is staleness-driven and
// bounded. A flat pass over every account on every fire would spend the month's
// provider allowance on accounts that did not move; the cutoff and the per-fire
// cap are what keep the sweep inside a metered plan (personal-a3v).
func TestSyncDueAccounts_StalenessCutoffAndBudget(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	s := &mockStore{}
	var gotCutoff time.Time
	var gotLimit int
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			gotCutoff = args.Get(1).(time.Time)
			gotLimit = args.Int(2)
		}).
		Return([]*entity.Account{}, nil).Once()

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)
	assert.Zero(t, report.Due)
	assert.Equal(t, now.Add(-defaultSweepMaxAge), gotCutoff, "the cutoff is explicit, not inherited from a cron interval")
	assert.Equal(t, defaultSweepAccountsPerFire, gotLimit)

	// Explicit opts win over the defaults.
	s2 := &mockStore{}
	s2.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			gotCutoff = args.Get(1).(time.Time)
			gotLimit = args.Int(2)
		}).
		Return([]*entity.Account{}, nil).Once()
	h2 := newHandler(s2).WithMarketDataClient(&mockMDClient{})
	_, err = h2.SyncDueAccounts(context.Background(), SweepOpts{Now: now, MaxAge: time.Hour, Limit: 7})
	require.NoError(t, err)
	assert.Equal(t, now.Add(-time.Hour), gotCutoff)
	assert.Equal(t, 7, gotLimit)
}

// TestSyncDueAccounts_FailureIsVisibleAndDoesNotStopTheSweep: on a schedule
// nobody reads a return value, so a failed account has to survive into the
// report — and must not take the accounts behind it down with it.
func TestSyncDueAccounts_FailureIsVisibleAndDoesNotStopTheSweep(t *testing.T) {
	broken := sweepAccount("11111111-1111-1111-1111-111111111111", "user-a", "dead-provider")
	fine := sweepAccount("22222222-2222-2222-2222-222222222222", "user-a", "hot")

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{broken, fine}, nil).Once()
	s.On("GetAccount", mock.Anything, broken.ID).Return(broken, nil)
	s.On("GetAccount", mock.Anything, fine.ID).Return(fine, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: "h1"}, nil)

	ws := &mockWalletSyncer{}
	// A wallet syncer error is reported per address, not raised: the failing
	// account still returns, with the failure in Errors.
	ws.On("SyncWallet", mock.Anything, "0x"+broken.ID, []string{"eth"}).
		Return([]entity.WalletBalance{}, errors.New("moralis 503"))
	ws.On("SyncWallet", mock.Anything, "0x"+fine.ID, []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "DAI", Amount: "100", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{})
	require.NoError(t, err)
	assert.Equal(t, 2, report.Synced, "a partial account still wrote its snapshot")
	require.Len(t, report.PartialAccounts, 1, "an account that could not speak for every balance is named")
	assert.Equal(t, broken.ID, report.PartialAccounts[0].AccountID)
	assert.Contains(t, report.PartialAccounts[0].Reason, "moralis 503")
}

// TestSyncDueAccounts_HardFailureIsCounted: an account whose sync returns an
// error (a missing address, a syncer that cannot be resolved) is counted and
// named rather than dropped from the run.
func TestSyncDueAccounts_HardFailureIsCounted(t *testing.T) {
	bad := sweepAccount("11111111-1111-1111-1111-111111111111", "user-a", "misconfigured")
	bad.Data = map[string]string{} // no address: SyncAccount refuses

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{bad}, nil).Once()
	s.On("GetAccount", mock.Anything, bad.ID).Return(bad, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).WithWalletSyncer(&mockWalletSyncer{})

	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Failed)
	assert.Zero(t, report.Synced)
	require.Len(t, report.Failures, 1)
	assert.Equal(t, bad.ID, report.Failures[0].AccountID)
	assert.Equal(t, "misconfigured", report.Failures[0].Name)
}

// TestSyncDueAccounts_ExpiredDeadlineNamesWhatItSkipped: the job's own timeout
// must not turn into a short run that reads as a complete one.
func TestSyncDueAccounts_ExpiredDeadlineNamesWhatItSkipped(t *testing.T) {
	acct := sweepAccount("11111111-1111-1111-1111-111111111111", "user-a", "hot")

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := h.SyncDueAccounts(ctx, SweepOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Failed)
	require.Len(t, report.Failures, 1)
	assert.Contains(t, report.Failures[0].Reason, "context canceled")
	s.AssertNotCalled(t, "GetAccount", mock.Anything, mock.Anything)
}

// TestCalculatePortfolioValue_ReportsAmountAge: a price and an amount go stale
// independently, and only the price has a sweep watching it. The total says how
// old the quantities under it are, so an hourly re-price cannot pass week-old
// amounts off as current.
func TestCalculatePortfolioValue_ReportsAmountAge(t *testing.T) {
	old := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 4, 6, 15, 0, 0, time.UTC)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: recent, Source: entity.SourceSync},
		{ID: "h2", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: old, Source: entity.SourceSync},
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Price{
		AssetId: testAssetID, BaseAssetId: "usd", Last: "100", Decimals: 0,
	}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId:  testPortfolioID,
		QuoteAssetId: "usd",
	}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Coverage.GetAmountsAsOf())
	assert.Equal(t, old, resp.Msg.Coverage.GetAmountsAsOf().AsTime(),
		"the oldest amount dates the whole total, however fresh the rest is")
}

// TestCalculatePortfolioValue_AmountAgeIgnoresUnsweptRows: a hand-entered amount
// is not stale, it is simply as old as its author left it, and nothing will ever
// refresh it. Counting it here pins the date forever, which is what made the
// field unreadable on production: it reported 26.07 for weeks while every synced
// account was being refreshed hourly.
func TestCalculatePortfolioValue_AmountAgeIgnoresUnsweptRows(t *testing.T) {
	ancient := time.Date(2025, 8, 1, 12, 0, 0, 0, time.UTC)
	old := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 4, 6, 15, 0, 0, time.UTC)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: recent, Source: entity.SourceSync},
		{ID: "h2", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: old, Source: entity.SourceManual},
		{ID: "h3", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: ancient, Source: entity.SourceLLMImport},
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Price{
		AssetId: testAssetID, BaseAssetId: "usd", Last: "100", Decimals: 0,
	}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId:  testPortfolioID,
		QuoteAssetId: "usd",
	}))
	require.NoError(t, err)
	assert.EqualValues(t, 3, resp.Msg.Coverage.GetPricedCount(),
		"unswept rows stay in the total; only their age is not a symptom")
	require.NotNil(t, resp.Msg.Coverage.GetAmountsAsOf())
	assert.Equal(t, recent, resp.Msg.Coverage.GetAmountsAsOf().AsTime(),
		"the date reports the sweep, so only synced rows can set it")
}

// TestCalculatePortfolioValue_AllUnsweptLeavesAmountsUndated: with nothing synced
// there is no sweep to report on, and the field says nothing rather than passing
// a manual entry date off as a confirmation. A caller tells this apart from an
// empty portfolio by priced_count.
func TestCalculatePortfolioValue_AllUnsweptLeavesAmountsUndated(t *testing.T) {
	old := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)

	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: testAssetID, Amount: decimal.NewFromInt(1), Decimals: 0, UpdatedAt: old, Source: entity.SourceManual},
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Price{
		AssetId: testAssetID, BaseAssetId: "usd", Last: "100", Decimals: 0,
	}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId:  testPortfolioID,
		QuoteAssetId: "usd",
	}))
	require.NoError(t, err)
	assert.EqualValues(t, 1, resp.Msg.Coverage.GetPricedCount())
	assert.Nil(t, resp.Msg.Coverage.GetAmountsAsOf(),
		"no synced amount stands behind this total, and silence beats a date that means something else")
}
