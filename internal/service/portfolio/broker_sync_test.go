package portfolio

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockBrokerSyncer struct {
	mock.Mock
}

func (m *mockBrokerSyncer) SyncBroker(ctx context.Context) ([]entity.BrokerPosition, entity.BrokerSkips, error) {
	args := m.Called(ctx)
	skips, _ := args.Get(1).(entity.BrokerSkips)
	if v := args.Get(0); v != nil {
		return v.([]entity.BrokerPosition), skips, args.Error(2)
	}
	return nil, skips, args.Error(2)
}

type mockBrokerSource struct {
	syncer entity.BrokerSyncer
	err    error
}

func (m *mockBrokerSource) BrokerSyncerForAccount(_ *entity.Account) (entity.BrokerSyncer, error) {
	return m.syncer, m.err
}

func brokerAccount() *entity.Account {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeBroker
	acct.Name = "T-Invest brokerage"
	acct.Data = map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "2000123456"}
	return acct
}

// TestSyncAccount_BrokerPositionsBecomeHoldings is the whole point of the
// ticket: a share, a fund, a bond and cash all reach holdings, and each is
// asked for under its OWN identity. Before this the resolver hardcoded
// cryptocurrency, so GAZP on moex and a GAZP token would have been one asset —
// and a share sent without a market is refused outright by the catalogue.
func TestSyncAccount_BrokerPositionsBecomeHoldings(t *testing.T) {
	acct := brokerAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Source == entity.SourceSync && h.AccountID == testAccountID
	})).Return(&entity.Holding{ID: testHoldingID}, nil).Times(4)

	syncer := &mockBrokerSyncer{}
	syncer.On("SyncBroker", mock.Anything).Return([]entity.BrokerPosition{
		{Ref: "BBG004730RP0", Symbol: "GAZP", Name: "Gazprom", Type: entity.AssetTypeStock, Market: "moex", Currency: "rub", Amount: "310000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
		{Ref: "BBG333333333", Symbol: "TMOS", Name: "Tinkoff iMOEX", Type: entity.AssetTypeFund, Market: "moex", Currency: "rub", Amount: "1000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
		{Ref: "TCS00A1055Y4", Symbol: "RU000A1055Y4", Name: "A bond", Type: entity.AssetTypeBond, Market: "moex", Currency: "rub", Amount: "5000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
		{Symbol: "USD", Name: "USD", Type: entity.AssetTypeForex, Market: "forex", Currency: "usd", Amount: "880000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
	}, entity.BrokerSkips{}, nil)

	md := &mockMDClient{}
	// Each expectation asserts one position's identity: type, market, and the
	// namespace its ref lives in. An unmatched call fails the test, which is
	// what makes these assertions rather than decoration.
	expectAsset(md, "GAZP", apiv1.AssetType_ASSET_TYPE_STOCK, "moex", "tinvest", "BBG004730RP0")
	expectAsset(md, "TMOS", apiv1.AssetType_ASSET_TYPE_FUND, "moex", "tinvest", "BBG333333333")
	expectAsset(md, "RU000A1055Y4", apiv1.AssetType_ASSET_TYPE_BOND, "moex", "tinvest", "TCS00A1055Y4")
	// Cash is the exception to identity-by-ref: the broker's id for a currency
	// line names a settlement instrument (figi USD800UTSTOM against ticker
	// USD000UTSTOM), so binding by it would tie dollars to a contract instead
	// of to the currency the rest of the system already prices.
	expectAsset(md, "USD", apiv1.AssetType_ASSET_TYPE_FOREX, "forex", "", "")
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(4), resp.Msg.HoldingsUpserted)
	assert.Equal(t, int32(0), resp.Msg.PositionsSkipped)
	assert.Equal(t, int32(0), resp.Msg.AssetsDefaultedMarket)
	s.AssertExpectations(t)
	md.AssertExpectations(t)
}

// expectAsset registers the FindOrCreateAsset call one position must make. The
// ref pair is asserted as absent when refSource is empty, so "no binding" is a
// checked outcome rather than an unchecked one.
func expectAsset(md *mockMDClient, symbol string, typ apiv1.AssetType, market, refSource, ref string) {
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.Symbol == symbol &&
			r.Msg.Type == typ &&
			r.Msg.GetMarket() == market &&
			r.Msg.GetExternalRefSource() == refSource &&
			r.Msg.GetExternalRef() == ref
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{
		Asset: &apiv1.Asset{Id: "asset-" + symbol}, Created: true,
	}), nil)
}

// TestSyncAccount_BrokerLockedIsItsOwnRow: a blocked part of a position is not
// spendable, and runway is the question the split exists to answer. The two
// rows carry the same asset and must sum to what the broker reported.
func TestSyncAccount_BrokerLockedIsItsOwnRow(t *testing.T) {
	acct := brokerAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	var written []*entity.Holding
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil).
		Run(func(args mock.Arguments) {
			written = append(written, args.Get(1).(*entity.Holding))
		}).Times(2)

	syncer := &mockBrokerSyncer{}
	syncer.On("SyncBroker", mock.Anything).Return([]entity.BrokerPosition{
		{Ref: "BBG00475K2X9", Symbol: "HYDR", Type: entity.AssetTypeStock, Market: "moex", Amount: "15000000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
		{Ref: "BBG00475K2X9", Symbol: "HYDR", Type: entity.AssetTypeStock, Market: "moex", Amount: "5000000000000", Decimals: 9, Liquidity: entity.LiquidityLocked},
	}, entity.BrokerSkips{}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	require.Equal(t, int32(2), resp.Msg.HoldingsUpserted)
	require.Len(t, written, 2)

	byLiquidity := map[entity.Liquidity]*entity.Holding{}
	for _, hld := range written {
		assert.Equal(t, "asset-HYDR", hld.AssetID)
		byLiquidity[hld.Liquidity] = hld
	}
	require.Contains(t, byLiquidity, entity.LiquidityLiquid)
	require.Contains(t, byLiquidity, entity.LiquidityLocked)
	sum := byLiquidity[entity.LiquidityLiquid].Amount.Add(byLiquidity[entity.LiquidityLocked].Amount)
	assert.Equal(t, "20000000000000", sum.String(), "the two rows must sum to the reported quantity")
}

// TestSyncAccount_BrokerSoldPositionIsZeroed: the zeroing rule comes free with
// the write path, and this is the acceptance criterion that says so. A position
// the broker stopped reporting goes to zero rather than living on.
func TestSyncAccount_BrokerSoldPositionIsZeroed(t *testing.T) {
	acct := brokerAccount()
	sold := testHolding("01926d35-6a1e-7006-8006-0000000000ff")
	sold.AssetID = "asset-SBER"
	sold.Source = entity.SourceSync
	sold.Liquidity = entity.LiquidityLiquid

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{sold}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)
	s.On("UpdateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.ID == sold.ID && h.Amount.IsZero()
	}), []string{"amount"}).Return(sold, nil)

	syncer := &mockBrokerSyncer{}
	syncer.On("SyncBroker", mock.Anything).Return([]entity.BrokerPosition{
		{Ref: "BBG004730RP0", Symbol: "GAZP", Type: entity.AssetTypeStock, Market: "moex", Amount: "310000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
	}, entity.BrokerSkips{}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.HoldingsZeroed)
	s.AssertExpectations(t)
}

// TestSyncAccount_BrokerSkipHoldsBackZeroing: a position the source returned
// and the adapter could not name is a position this snapshot does not speak
// for. Zeroing anything on such a snapshot would erase a holding that is still
// held — delisted paper leaves the catalogue while the shares stay in the
// account — so removal is withheld and the count says why.
func TestSyncAccount_BrokerSkipHoldsBackZeroing(t *testing.T) {
	acct := brokerAccount()
	stale := testHolding("01926d35-6a1e-7006-8006-0000000000fe")
	stale.AssetID = "asset-OLD"
	stale.Source = entity.SourceSync
	stale.Liquidity = entity.LiquidityLiquid

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{stale}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	syncer := &mockBrokerSyncer{}
	syncer.On("SyncBroker", mock.Anything).Return([]entity.BrokerPosition{
		{Ref: "BBG004730RP0", Symbol: "GAZP", Type: entity.AssetTypeStock, Market: "moex", Amount: "310000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
	}, entity.BrokerSkips{UnknownInstrument: 1, Unparsable: 1}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.Msg.PositionsSkipped)
	assert.Equal(t, int32(0), resp.Msg.HoldingsZeroed, "a snapshot missing a position may not remove one")
	s.AssertNotCalled(t, "UpdateHolding", mock.Anything, mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerDefaultedMarketIsCountedNotWithheld: a market inferred
// from the board or the currency is the one guess this path makes. The position
// IS written — refusing it would leave stocks near-empty, which is the zero
// this work exists to end — and the count is what keeps the guess visible.
// Unlike a skip it does not cost the snapshot its right to remove positions:
// nothing is missing from it.
func TestSyncAccount_BrokerDefaultedMarketIsCountedNotWithheld(t *testing.T) {
	acct := brokerAccount()
	sold := testHolding("01926d35-6a1e-7006-8006-0000000000fd")
	sold.AssetID = "asset-SBER"
	sold.Source = entity.SourceSync
	sold.Liquidity = entity.LiquidityLiquid

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{sold}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)
	s.On("UpdateHolding", mock.Anything, mock.Anything, []string{"amount"}).Return(sold, nil)

	syncer := &mockBrokerSyncer{}
	syncer.On("SyncBroker", mock.Anything).Return([]entity.BrokerPosition{
		{Ref: "US5543821012", Symbol: "US5543821012", Type: entity.AssetTypeStock, Market: "spbex", Amount: "1000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid},
	}, entity.BrokerSkips{DefaultedMarket: 1}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.AssetsDefaultedMarket)
	assert.Equal(t, int32(0), resp.Msg.PositionsSkipped, "a defaulted market is a guess, not a gap")
	assert.Equal(t, int32(1), resp.Msg.HoldingsZeroed)
}

// TestSyncAccount_BrokerFetchErrorKeepsPositions: a token revoked or a broker
// down must not read as an emptied account.
func TestSyncAccount_BrokerFetchErrorKeepsPositions(t *testing.T) {
	acct := brokerAccount()
	held := testHolding("01926d35-6a1e-7006-8006-0000000000fc")
	held.Source = entity.SourceSync

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{held}, "", nil)

	syncer := &mockBrokerSyncer{}
	syncer.On("SyncBroker", mock.Anything).Return(nil, entity.BrokerSkips{}, errors.New("40003: token is not valid"))

	md := &mockMDClient{autoAsset: true}

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.HoldingsZeroed)
	require.NotEmpty(t, resp.Msg.Errors)
	s.AssertNotCalled(t, "UpdateHolding", mock.Anything, mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerNoAdapter: an account whose provider slug no adapter
// answers to is refused by name, not by the generic "cannot be synced" that
// every broker account used to get.
func TestSyncAccount_BrokerNoAdapter(t *testing.T) {
	acct := brokerAccount()
	acct.Data["provider"] = "some-other-broker"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).WithBrokerSyncerSource(&mockBrokerSource{syncer: nil})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestSyncAccount_BrokerMisconfiguredAccountIsNotAServerFault: every way the
// factory refuses is the account's own stored configuration — no broker account
// named, a malformed host, a missing trust anchor. Internal would tell the
// caller to retry a thing retrying cannot fix, and would be counted as a 5xx by
// whatever watches the RPC.
func TestSyncAccount_BrokerMisconfiguredAccountIsNotAServerFault(t *testing.T) {
	acct := brokerAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).
		WithBrokerSyncerSource(&mockBrokerSource{err: errors.New("account names no broker account: set data[\"broker_account_id\"]")})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "broker_account_id", "the message must name the field to fix")
}

// TestSyncAccount_BrokerNotConfigured: a deployment without broker syncing says
// so, rather than reporting the account type as unsyncable.
func TestSyncAccount_BrokerNotConfigured(t *testing.T) {
	acct := brokerAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}
