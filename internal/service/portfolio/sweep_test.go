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
	"github.com/foxcool/greedy-eye/internal/store"
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
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
	assert.Equal(t, 2, report.Picked)
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
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			gotCutoff = args.Get(1).(time.Time)
			gotLimit = args.Int(3)
		}).
		Return([]*entity.Account{}, nil).Once()

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)
	assert.Zero(t, report.Picked)
	assert.Equal(t, now.Add(-defaultSweepMaxAge), gotCutoff, "the cutoff is explicit, not inherited from a cron interval")
	assert.Equal(t, defaultSweepAccountsPerFire, gotLimit)

	// Explicit opts win over the defaults.
	s2 := &mockStore{}
	s2.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			gotCutoff = args.Get(1).(time.Time)
			gotLimit = args.Int(3)
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
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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

// TestSweep_AccountLeftNoFresherIsStoodDown is the fix for the two days the
// crypto portfolio spent on stale quantities: the sweep orders by last SUCCESS,
// so a sync that writes nothing leaves the account exactly as stale as it found
// it and it is picked again next hour, forever.
//
// The trigger is "no fresher", not "failed". The outage that bought this
// returned 200 with the provider's 401 inside the response body — the sweep
// counted those runs as synced.
func TestSweep_AccountLeftNoFresherIsStoodDown(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("RecordSyncMiss", mock.Anything, testAccountID, now, missBackoffBase, missBackoffCap).
		Return(1, now.Add(missBackoffBase), nil).Once()

	// A source that answers and reports nothing but its own failure.
	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).
		Return([]entity.WalletBalance{}, errors.New("moralis API status 401"))

	md := &mockMDClient{}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)
	_, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)

	s.AssertExpectations(t)
	s.AssertNotCalled(t, "ClearSyncDeferral", mock.Anything, testAccountID)
}

// TestSweep_EmptyWalletIsNotStoodDown: a wallet holding nothing writes no
// holdings either, and that is a true answer rather than a silence. Deferring
// it would punish an account for being empty and eventually stop refreshing the
// one thing that would show it had stopped being empty.
func TestSweep_EmptyWalletIsNotStoodDown(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("ClearSyncDeferral", mock.Anything, testAccountID).Return(nil)
	// Declared so that a call would be RECORDED. Without an expectation the
	// lenient mock returns without registering anything and AssertNotCalled
	// below passes whatever the code does — a test that cannot go red.
	s.On("RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(1, now, nil).Maybe()

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).
		Return([]entity.WalletBalance{}, nil) // no balances, no complaint

	md := &mockMDClient{}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)
	_, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)

	s.AssertNotCalled(t, "RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestSweep_PartialSyncIsNotStoodDown: some chains answered, so the account IS
// fresher and its errors are disclosure rather than failure. Standing it down
// would be the mirror of the bug — punishing the accounts that partly work
// while the fully broken ones are already deferred.
func TestSweep_PartialSyncIsNotStoodDown(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: "h1"}, nil)
	s.On("ClearSyncDeferral", mock.Anything, testAccountID).Return(nil)
	s.On("RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(1, now, nil).Maybe()

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).Return(
		[]entity.WalletBalance{{Symbol: "ETH", Name: "Ethereum", Amount: "100", Decimals: 18, Chain: "eth"}},
		errors.New("chain base: provider timeout"))

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.Anything).Return(
		connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: "asset-eth"}}), nil)
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)
	_, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)

	s.AssertNotCalled(t, "RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestSyncAccount_SuccessClearsTheDeferral: a person who repairs a credential
// and presses sync has said "it works now" in the most direct way available.
// Backoff earned while it was broken must not outlive the repair — the lesson
// the price path paid for twice (personal-edtu, personal-7994).
func TestSyncAccount_SuccessClearsTheDeferral(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: "h1"}, nil)
	s.On("ClearSyncDeferral", mock.Anything, testAccountID).Return(nil).Once()

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "ETH", Name: "Ethereum", Amount: "100", Decimals: 18, Chain: "eth"},
	}, nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.Anything).Return(
		connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: "asset-eth"}}), nil)
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)
	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestSweep_HardFailureIsStoodDown covers the other arm: a sync that fails at
// the RPC level never reaches the "no fresher" test, and an account that cannot
// even be read is exactly the kind that would otherwise hold a slot forever.
func TestSweep_HardFailureIsStoodDown(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()
	// The account vanishes between selection and sync — deleted, or the store
	// is unwell. Either way the sweep must not keep offering it every hour.
	s.On("GetAccount", mock.Anything, testAccountID).Return(nil, store.ErrNotFound)
	s.On("RecordSyncMiss", mock.Anything, testAccountID, now, missBackoffBase, missBackoffCap).
		Return(1, now.Add(missBackoffBase), nil).Once()

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})
	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)

	assert.Equal(t, 1, report.Failed)
	s.AssertExpectations(t)
}

// TestSweep_ReportSeparatesStaleFromPicked is the line that let the starvation
// run for two days. It said "due 2" — which was the LIMIT — while twelve
// accounts waited, so a queue nobody was getting through read as a quiet
// instance with little to do.
func TestSweep_ReportSeparatesStaleFromPicked(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	first := sweepAccount("acct-1", testUserID, "first")
	second := sweepAccount("acct-2", testUserID, "second")

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{first, second}, nil).Once()
	s.On("CountDueSyncTargets", mock.Anything, mock.Anything, mock.Anything).Return(12, nil).Once()
	s.On("GetAccount", mock.Anything, mock.Anything).Return(nil, store.ErrNotFound)
	s.On("RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(1, now, nil).Maybe()

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})
	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)

	assert.Equal(t, 2, report.Picked, "what the budget allowed")
	assert.Equal(t, 12, report.Stale, "what was waiting — the number the old line hid")
	s.AssertExpectations(t)
}

// TestSweep_ReportSurvivesAnUncountableQueue: the count is a separate query and
// can fail on its own. A sweep that refuses to run because it could not count
// what is waiting would trade a reporting problem for an outage.
func TestSweep_ReportSurvivesAnUncountableQueue(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{}, nil).Once()
	s.On("CountDueSyncTargets", mock.Anything, mock.Anything, mock.Anything).
		Return(0, errors.New("statement timeout")).Once()

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})
	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{Now: now})

	require.NoError(t, err, "the sweep still ran")
	assert.Equal(t, -1, report.Stale, "unknown is reported as unknown, not as zero")
}

// TestGetAccountSweepSchedule_ReportsWhoIsHeldBackAndWhoIsWaiting: a deferral
// nobody can see is the degradation this change exists to prevent, one layer up.
func TestGetAccountSweepSchedule_ReportsWhoIsHeldBackAndWhoIsWaiting(t *testing.T) {
	synced := time.Date(2026, 8, 31, 15, 30, 0, 0, time.UTC)
	until := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)

	s := &mockStore{}
	s.On("ListSyncDeferrals", mock.Anything, testUserID, "").Return([]*entity.SyncDeferral{
		{AccountID: "acct-1", AccountName: "eth-cold", LastSyncedAt: &synced, Misses: 3, NextAttemptAt: until},
		{AccountID: "acct-2", AccountName: "never synced", Misses: 1, NextAttemptAt: until},
	}, nil).Once()
	s.On("CountDueSyncTargets", mock.Anything, mock.Anything, mock.Anything).Return(12, nil).Once()

	h := newHandler(s)
	resp, err := h.GetAccountSweepSchedule(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.GetAccountSweepScheduleRequest{}))
	require.NoError(t, err)

	require.Len(t, resp.Msg.Accounts, 2)
	assert.Equal(t, "eth-cold", resp.Msg.Accounts[0].AccountName)
	assert.Equal(t, uint32(3), resp.Msg.Accounts[0].Misses)
	assert.Equal(t, until, resp.Msg.Accounts[0].NextAttemptAt.AsTime())
	assert.Equal(t, synced, resp.Msg.Accounts[0].LastSyncedAt.AsTime())
	assert.Nil(t, resp.Msg.Accounts[1].LastSyncedAt,
		"never synced is absent, not epoch — the stalest state there is must not render as 1970")
	assert.Equal(t, uint32(12), resp.Msg.DueNow, "the queue behind the deferrals")
	s.AssertExpectations(t)
}

// TestResetAccountSweepSchedule_RefusesAnEmptyList: forgiving everything is a
// bigger statement than forgiving one, and nobody gets it by omitting a field.
// The price-path twin refuses the same way.
func TestResetAccountSweepSchedule_RefusesAnEmptyList(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)

	_, err := h.ResetAccountSweepSchedule(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.ResetAccountSweepScheduleRequest{}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	s.AssertNotCalled(t, "ClearSyncDeferrals", mock.Anything, mock.Anything, mock.Anything)
}

// TestResetAccountSweepSchedule_CountsWhatOwedNothing: an account that was not
// deferred is not an error. The count is how the caller learns the schedule was
// not what was wrong — which is the question an operator is actually asking.
func TestResetAccountSweepSchedule_CountsWhatOwedNothing(t *testing.T) {
	s := &mockStore{}
	s.On("ClearSyncDeferrals", mock.Anything, testUserID, []string{"acct-1", "acct-2"}).
		Return(1, nil).Once()

	h := newHandler(s)
	resp, err := h.ResetAccountSweepSchedule(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.ResetAccountSweepScheduleRequest{
			AccountIds: []string{"acct-1", "acct-2"},
		}))
	require.NoError(t, err)

	assert.Equal(t, uint32(1), resp.Msg.AccountsFreed, "two named, one owed anything")
	s.AssertExpectations(t)
}

// TestGetAccountSweepSchedule_NeedsAUser: a deferral is operational detail about
// somebody's credential, not catalogue, so it is never served without an owner.
func TestGetAccountSweepSchedule_NeedsAUser(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.GetAccountSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.GetAccountSweepScheduleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
