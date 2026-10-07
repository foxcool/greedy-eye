package portfolio

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type mockBrokerLister struct {
	refs []entity.BrokerAccountRef
	err  error
	// calls counts how often the broker was asked, because asking once per
	// sync is the difference between a fan-out and a fan-out per account.
	calls int
}

func (m *mockBrokerLister) ListBrokerAccounts(_ context.Context) ([]entity.BrokerAccountRef, error) {
	m.calls++
	return m.refs, m.err
}

type mockBrokerListerSource struct {
	lister entity.BrokerAccountLister
	err    error
}

func (m *mockBrokerListerSource) BrokerAccountListerForAccount(_ *entity.Account) (entity.BrokerAccountLister, error) {
	return m.lister, m.err
}

// syncerPerAccount hands out a different syncer per broker account id, which is
// what the real resolver does and what a fan-out has to get right: one syncer
// serving every account would make merged and separate results identical.
type syncerPerAccount struct {
	byBrokerID map[string]entity.BrokerSyncer
	err        error
}

func (s *syncerPerAccount) BrokerSyncerForAccount(a *entity.Account) (entity.BrokerSyncer, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byBrokerID[a.Data[entity.BrokerAccountDataKey]], nil
}

// tokenAccount is the account that holds the credentials and names no broker
// account of its own — the shape this whole path exists for.
func tokenAccount() *entity.Account {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeBroker
	acct.Name = "T-Invest"
	acct.PortfolioID = testPortfolioID
	acct.Capabilities = []entity.AccountCapability{entity.CapabilityPortfolioSync}
	acct.SystemScopes = []entity.AccountCapability{entity.CapabilityMarketData}
	acct.Data = map[string]string{"provider": "tinvest", "api_key": "t", "root_ca": "PEM"}
	return acct
}

func positionOf(symbol, ref, amount string) []entity.BrokerPosition {
	return []entity.BrokerPosition{{
		Ref: ref, Symbol: symbol, Type: entity.AssetTypeStock, Market: "moex",
		Amount: amount, Decimals: 9, Liquidity: entity.LiquidityLiquid,
	}}
}

func brokerSyncerReturning(positions []entity.BrokerPosition) *mockBrokerSyncer {
	s := &mockBrokerSyncer{}
	s.On("SyncBroker", mock.Anything).Return(positions, entity.BrokerSkips{}, nil)
	return s
}

// TestSyncAccount_BrokerTokenSyncsEveryAccountItReaches is the ticket in one
// test: an account carrying only a token pulls positions from every account
// that token opens, and each of them becomes an account here rather than one
// merged pile.
func TestSyncAccount_BrokerTokenSyncsEveryAccountItReaches(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent}, "", nil)

	// The store echoes the stored row back, id and all, which is what the
	// handler then syncs by — a stub with no type would be refused by the
	// dispatcher and the fan-out would report two failures instead of two
	// accounts.
	var createdAccounts []*entity.Account
	for _, brokerID := range []string{"2000000001", "2000000002"} {
		id := brokerID
		stored := &entity.Account{
			ID: "acct-" + id, UserID: testUserID, Type: entity.AccountTypeBroker,
			Name: "T-Invest · Брокерский счёт (" + id + ")", PortfolioID: testPortfolioID,
			Data: map[string]string{"provider": "tinvest", "api_key": "t", "root_ca": "PEM", entity.BrokerAccountDataKey: id},
		}
		s.On("CreateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool {
			return a.Data[entity.BrokerAccountDataKey] == id
		})).Return(stored, nil).Once().Run(func(args mock.Arguments) {
			// Captured as ASKED, not as stored: the assertions below are about
			// what this code decided to create.
			createdAccounts = append(createdAccounts, args.Get(1).(*entity.Account))
		})
	}
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
		{ID: "2000000002", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
		{ID: "2000000003", Name: "Копилка", NotSyncableReason: "a round-up savings pot, not a brokerage account", ReadOnly: true},
	}}
	syncers := &syncerPerAccount{byBrokerID: map[string]entity.BrokerSyncer{
		"2000000001": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "90000000000")),
		"2000000002": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "310000000000")),
	}}

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).
		WithBrokerSyncerSource(syncers).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)

	// Two accounts created, the savings pot passed over, and 90 + 310 written
	// as two holdings rather than one row of 400 — which is the reason the
	// accounts stay apart at all.
	assert.Equal(t, int32(2), resp.Msg.AccountsCreated)
	assert.Equal(t, int32(2), resp.Msg.HoldingsUpserted)
	require.Len(t, createdAccounts, 2)
	assert.Equal(t, 1, lister.calls, "the broker is asked once per sync, not once per account")

	for _, a := range createdAccounts {
		assert.Equal(t, testUserID, a.UserID, "the owner is the one whose token found it")
		assert.Equal(t, testPortfolioID, a.PortfolioID, "positions land in the portfolio the operator already chose")
		assert.Equal(t, entity.AccountTypeBroker, a.Type)
		assert.Equal(t, "t", a.Data["api_key"], "the account cannot sync without the credentials")
		assert.Empty(t, a.SystemScopes, "system scopes are admin-managed and never inherited")
		assert.Contains(t, a.Name, a.Data[entity.BrokerAccountDataKey], "three identically named accounts need the id to tell them apart")
	}
	assert.NotEqual(t, createdAccounts[0].Data["broker_account_id"], createdAccounts[1].Data["broker_account_id"])
}

// TestSyncAccount_BrokerDiscoveryIsIdempotent: discovery runs as a side effect
// of syncing, so a second sync must create nothing. Without this the button
// grows a new account every time somebody presses it.
func TestSyncAccount_BrokerDiscoveryIsIdempotent(t *testing.T) {
	parent := tokenAccount()
	child := tokenAccount()
	child.ID = "acct-child"
	child.Name = "T-Invest · Брокерский счёт (2000000001)"
	child.Data = map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "2000000001"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent, child}, "", nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
	}}
	syncers := &syncerPerAccount{byBrokerID: map[string]entity.BrokerSyncer{
		"2000000001": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "90000000000")),
	}}

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).
		WithBrokerSyncerSource(syncers).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.AccountsCreated)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	s.AssertNotCalled(t, "CreateAccount", mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerRepeatedAccountIsCreatedOnce: the listing comes off the
// network and nothing here controls it. An account named twice used to pass the
// "does it exist" check twice — the map is a snapshot taken before the loop —
// and mint two accounts for one broker account, which then synced the same
// position twice. That is lying in the plus, the direction that costs money.
func TestSyncAccount_BrokerRepeatedAccountIsCreatedOnce(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent}, "", nil)
	stored := &entity.Account{
		ID: "acct-2000000001", UserID: testUserID, Type: entity.AccountTypeBroker,
		Name: "T-Invest · Брокерский счёт (2000000001)", PortfolioID: testPortfolioID,
		Data: map[string]string{"provider": "tinvest", "api_key": "t", entity.BrokerAccountDataKey: "2000000001"},
	}
	s.On("CreateAccount", mock.Anything, mock.Anything).Return(stored, nil).Once()
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
	}}
	syncers := &syncerPerAccount{byBrokerID: map[string]entity.BrokerSyncer{
		"2000000001": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "90000000000")),
	}}

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).
		WithBrokerSyncerSource(syncers).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.AccountsCreated, "one broker account is one account here, however often it is listed")
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted, "90 shares listed twice are still 90 shares")
	s.AssertNumberOfCalls(t, "CreateAccount", 1)
}

// TestSyncAccount_BrokerRefusesAnUnbelievableListing: the listing arrives from a
// host the account itself names, so its length is not this system's to trust. A
// answer long enough to fill the accounts table is refused whole — half of an
// unbelievable answer is still unbelievable.
func TestSyncAccount_BrokerRefusesAnUnbelievableListing(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)

	refs := make([]entity.BrokerAccountRef, 0, maxDiscoveredAccounts+1)
	for i := 0; i <= maxDiscoveredAccounts; i++ {
		refs = append(refs, entity.BrokerAccountRef{
			ID: fmt.Sprintf("2%09d", i), Name: "flood", Syncable: true, ReadOnly: true,
		})
	}

	h := newHandler(s).WithMarketDataClient(&mockMDClient{autoAsset: true}).
		WithBrokerSyncerSource(&syncerPerAccount{}).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: &mockBrokerLister{refs: refs}})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.AccountsCreated)
	require.NotEmpty(t, resp.Msg.Errors)
	assert.Contains(t, resp.Msg.Errors[0], "nothing was created")
	s.AssertNotCalled(t, "CreateAccount", mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "ListAccounts", mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerLeavesAnotherPortfoliosAccountAlone: two tokens can see
// the same broker account, and the row bound to another portfolio is neither
// rewritten nor duplicated. Rewriting would change positions the caller never
// named; duplicating would put the same money in two portfolios.
func TestSyncAccount_BrokerLeavesAnotherPortfoliosAccountAlone(t *testing.T) {
	parent := tokenAccount()
	elsewhere := tokenAccount()
	elsewhere.ID = "acct-elsewhere"
	elsewhere.Name = "T-Invest in another portfolio"
	elsewhere.PortfolioID = "01926d35-6a1e-7002-8002-0000000000ff"
	elsewhere.Data = map[string]string{"provider": "tinvest", "api_key": "other", entity.BrokerAccountDataKey: "2000000001"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent, elsewhere}, "", nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
	}}

	h := newHandler(s).WithMarketDataClient(&mockMDClient{autoAsset: true}).
		WithBrokerSyncerSource(&syncerPerAccount{}).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.AccountsCreated, "the money must not appear in two portfolios")
	assert.Equal(t, int32(0), resp.Msg.HoldingsUpserted, "the other portfolio's positions are not this call's to rewrite")
	require.NotEmpty(t, resp.Msg.Errors)
	assert.Contains(t, resp.Msg.Errors[0], "another portfolio")
	s.AssertNotCalled(t, "CreateAccount", mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerOneAccountFailingKeepsTheOthers: each account is its
// own call to the broker, so one refusal says nothing about the rest. Aborting
// would let a single revoked permission hide a healthy portfolio.
func TestSyncAccount_BrokerOneAccountFailingKeepsTheOthers(t *testing.T) {
	parent := tokenAccount()
	good := tokenAccount()
	good.ID = "acct-good"
	good.Name = "good"
	good.Data = map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "2000000001"}
	bad := tokenAccount()
	bad.ID = "acct-bad"
	bad.Name = "bad"
	bad.Data = map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "2000000002"}

	failing := &mockBrokerSyncer{}
	failing.On("SyncBroker", mock.Anything).Return(nil, entity.BrokerSkips{}, errors.New("30079: instrument is not available"))

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent, good, bad}, "", nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "one", Syncable: true, ReadOnly: true},
		{ID: "2000000002", Name: "two", Syncable: true, ReadOnly: true},
	}}
	syncers := &syncerPerAccount{byBrokerID: map[string]entity.BrokerSyncer{
		"2000000001": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "90000000000")),
		"2000000002": failing,
	}}

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).
		WithBrokerSyncerSource(syncers).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted, "the healthy account still synced")
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "bad", "an error nobody can attribute to an account is not an error anybody can act on")
}

// TestSyncAccount_BrokerTokenReachingNothingSyncableSaysSo: an empty success
// and a sync that did nothing look identical from outside. Only one of them is
// the truth here.
func TestSyncAccount_BrokerTokenReachingNothingSyncableSaysSo(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent}, "", nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000003", Name: "Копилка", NotSyncableReason: "a round-up savings pot, not a brokerage account"},
	}}

	h := newHandler(s).WithMarketDataClient(&mockMDClient{autoAsset: true}).
		WithBrokerSyncerSource(&syncerPerAccount{}).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.AccountsCreated)
	require.NotEmpty(t, resp.Msg.Errors)
	assert.Contains(t, resp.Msg.Errors[0], "no syncable account")
	s.AssertNotCalled(t, "CreateAccount", mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerListingFailureIsHard: without the listing there is
// nothing to sync and nothing to say, and a zero-account success would read as
// "the token reaches nothing" — which is a different fact entirely.
func TestSyncAccount_BrokerListingFailureIsHard(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)

	lister := &mockBrokerLister{err: errors.New("40003: token is not valid")}

	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).
		WithBrokerSyncerSource(&syncerPerAccount{}).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	s.AssertNotCalled(t, "CreateAccount", mock.Anything, mock.Anything)
}

// TestSyncAccount_BrokerDiscoveryUnavailableSaysWhatToDo: a build that cannot
// ask the broker must send the operator to the field they can fill, not report
// the account as unsyncable.
func TestSyncAccount_BrokerDiscoveryUnavailableSaysWhatToDo(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).
		WithBrokerSyncerSource(&syncerPerAccount{})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Contains(t, err.Error(), entity.BrokerAccountDataKey)
}

// TestSyncAccount_BrokerNamedAccountDoesNotFanOut: an account that names its
// own broker account is the case lab0 shipped, and discovery must not touch it.
func TestSyncAccount_BrokerNamedAccountDoesNotFanOut(t *testing.T) {
	acct := tokenAccount()
	acct.Data["broker_account_id"] = "2000000001"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	lister := &mockBrokerLister{}
	syncers := &syncerPerAccount{byBrokerID: map[string]entity.BrokerSyncer{
		"2000000001": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "90000000000")),
	}}

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).
		WithBrokerSyncerSource(syncers).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	assert.Equal(t, int32(0), resp.Msg.AccountsCreated)
	assert.Equal(t, 0, lister.calls, "an account that names its own broker account asks nobody")
	s.AssertNotCalled(t, "ListAccounts", mock.Anything, mock.Anything)
}

// discoveredCopy is a broker account opened by discovery: the parent's data
// cloned, plus the broker account id it syncs.
func discoveredCopy(id, brokerID string, data map[string]string) *entity.Account {
	a := testAccount(id)
	a.Type = entity.AccountTypeBroker
	a.Data = maps.Clone(data)
	a.Data[entity.BrokerAccountDataKey] = brokerID
	return a
}

func rotateTokenRequest(newToken string) *connect.Request[apiv1.UpdateAccountRequest] {
	return connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account: &apiv1.Account{
			Id:   testAccountID,
			Data: map[string]string{"provider": "tinvest", "api_key": newToken, "root_ca": "PEM"},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data"}},
	})
}

// TestUpdateAccount_RotationReachesDiscoveredCopies: discovery copies the token
// into every account it opens, so pasting a new token into the account a
// person knows must reach the copies too — otherwise they keep sending the
// revoked one on every sync (personal-9m3i). Only the accounts holding the old
// value are touched: a different token, or the same token at another
// provider, is somebody else's credential.
func TestUpdateAccount_RotationReachesDiscoveredCopies(t *testing.T) {
	parent := tokenAccount()
	copyA := discoveredCopy("019a0000-0000-7000-8000-00000000000a", "2052372295", parent.Data)
	copyB := discoveredCopy("019a0000-0000-7000-8000-00000000000b", "2285710299", parent.Data)
	otherToken := discoveredCopy("019a0000-0000-7000-8000-00000000000c", "9999", map[string]string{"provider": "tinvest", "api_key": "other"})
	otherProvider := discoveredCopy("019a0000-0000-7000-8000-00000000000d", "8888", map[string]string{"provider": "finam", "api_key": "t"})

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool { return a.ID == testAccountID }), []string{"data"}).
		Return(parent, nil).Once()
	s.On("ListAccounts", mock.Anything, mock.MatchedBy(func(o ListAccountsOpts) bool {
		return o.UserID == testUserID && o.Type == entity.AccountTypeBroker
	})).Return([]*entity.Account{parent, copyA, copyB, otherToken, otherProvider}, "", nil)
	for _, c := range []*entity.Account{copyA, copyB} {
		id, brokerID := c.ID, c.Data[entity.BrokerAccountDataKey]
		s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool {
			return a.ID == id && a.Data["api_key"] == "t-new" &&
				a.Data[entity.BrokerAccountDataKey] == brokerID && a.Data["root_ca"] == "PEM"
		}), []string{"data"}).Return(c, nil).Once()
	}

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), rotateTokenRequest("t-new"))
	require.NoError(t, err)
	s.AssertExpectations(t)
	s.AssertNumberOfCalls(t, "UpdateAccount", 3) // the parent and its two copies, nothing else
}

// TestUpdateAccount_RotationNamesCopiesItCouldNotReach: a copy that cannot be
// updated stops the rotation before the edited row is written, and the answer
// names it. The row keeping the old value is what makes a retry work: the next
// attempt still sees a change, finds the straggler by the old value, and
// finishes. Written the other way round, the retry reported success over a
// copy still sending the revoked token.
func TestUpdateAccount_RotationNamesCopiesItCouldNotReach(t *testing.T) {
	parent := tokenAccount()
	stuck := discoveredCopy("019a0000-0000-7000-8000-00000000000e", "2052372295", parent.Data)
	isParent := mock.MatchedBy(func(a *entity.Account) bool { return a.ID == testAccountID })
	isStuck := mock.MatchedBy(func(a *entity.Account) bool { return a.ID == stuck.ID && a.Data["api_key"] == "t-new" })

	first := &mockStore{}
	first.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	first.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{stuck}, "", nil)
	first.On("UpdateAccount", mock.Anything, isStuck, []string{"data"}).Return(nil, errors.New("connection reset")).Once()

	_, err := newHandler(first).UpdateAccount(ctxWithUser(testUserID), rotateTokenRequest("t-new"))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	assert.Contains(t, err.Error(), stuck.ID)
	assert.Contains(t, err.Error(), "still hold the old secret")
	first.AssertNotCalled(t, "UpdateAccount", mock.Anything, isParent, mock.Anything)

	// The retry: the row still holds "t", the straggler too.
	retry := &mockStore{}
	retry.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	retry.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{stuck}, "", nil)
	retry.On("UpdateAccount", mock.Anything, isStuck, []string{"data"}).Return(stuck, nil).Once()
	retry.On("UpdateAccount", mock.Anything, isParent, []string{"data"}).Return(parent, nil).Once()

	_, err = newHandler(retry).UpdateAccount(ctxWithUser(testUserID), rotateTokenRequest("t-new"))
	require.NoError(t, err)
	retry.AssertExpectations(t)
}

// TestUpdateAccount_NoRotationWithoutChangedSecret: renaming, re-saving the
// masked form, or editing a non-broker account reads nothing beyond the row.
func TestUpdateAccount_NoRotationWithoutChangedSecret(t *testing.T) {
	parent := tokenAccount()
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("UpdateAccount", mock.Anything, mock.Anything, []string{"data"}).Return(parent, nil).Once()

	// The form echoes the masked token back: restored, so not a change.
	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), rotateTokenRequest(maskPrefix))
	require.NoError(t, err)
	s.AssertNotCalled(t, "ListAccounts", mock.Anything, mock.Anything)

	exchange := testAccount(testAccountID)
	exchange.Data = map[string]string{"provider": "binance", "api_key": "old"}
	e := &mockStore{}
	e.On("GetAccount", mock.Anything, testAccountID).Return(exchange, nil)
	e.On("UpdateAccount", mock.Anything, mock.Anything, []string{"data"}).Return(exchange, nil).Once()
	_, err = newHandler(e).UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account:    &apiv1.Account{Id: testAccountID, Data: map[string]string{"provider": "binance", "api_key": "new"}},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data"}},
	}))
	require.NoError(t, err)
	e.AssertNotCalled(t, "ListAccounts", mock.Anything, mock.Anything)
}

// TestUpdateAccount_RefusedRequestRotatesNothing: a request the capability
// invariant rejects must not have moved another account's secret. The store
// checks the invariant only on write, which now comes after the rotation, so
// the handler asks it first.
func TestUpdateAccount_RefusedRequestRotatesNothing(t *testing.T) {
	parent := tokenAccount()
	parent.Capabilities = []entity.AccountCapability{entity.CapabilityPortfolioSync, entity.CapabilityMarketData}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account: &apiv1.Account{
			Id:           testAccountID,
			Data:         map[string]string{"provider": "tinvest", "api_key": "t-new", "root_ca": "PEM"},
			Capabilities: []string{string(entity.CapabilityManualPositions)}, // not allowed on a broker account
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data", "capabilities"}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	s.AssertNotCalled(t, "ListAccounts", mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "UpdateAccount", mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateAccount_FailedSaveNamesCopiesAlreadyRotated: when the copies took
// the new secret and the edited row's own write then fails, the credential is
// split — and the answer must say so instead of reading as "nothing happened".
func TestUpdateAccount_FailedSaveNamesCopiesAlreadyRotated(t *testing.T) {
	parent := tokenAccount()
	moved := discoveredCopy("019a0000-0000-7000-8000-00000000000f", "2052372295", parent.Data)

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{moved}, "", nil)
	s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool { return a.ID == moved.ID }), []string{"data"}).
		Return(moved, nil).Once()
	s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool { return a.ID == testAccountID }), []string{"data"}).
		Return(nil, errors.New("connection reset")).Once()

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), rotateTokenRequest("t-new"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already carry the new secret")
	assert.Contains(t, err.Error(), moved.ID)
	s.AssertExpectations(t)
}

// TestSyncAccount_BrokerRaceLoserSyncsTheWinnersAccount is personal-gskb from
// the side that loses. Two syncs of one token both read "no such account" and
// both create; the unique index refuses the second. That sync must take the row
// the first one made — a second success for the person who double-clicked, and
// one account, not two, in every total.
func TestSyncAccount_BrokerRaceLoserSyncsTheWinnersAccount(t *testing.T) {
	parent := tokenAccount()
	winner := tokenAccount()
	winner.ID = "acct-winner"
	winner.Name = "T-Invest · Брокерский счёт (2000000001)"
	winner.Data = map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "2000000001"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	// The snapshot is taken before the winner commits; the re-read after it.
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent}, "", nil).Once()
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent, winner}, "", nil)
	s.On("CreateAccount", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("%w: tinvest broker account 2000000001 already belongs to account acct-winner", store.ErrAlreadyExists)).Once()
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil)

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
	}}
	syncers := &syncerPerAccount{byBrokerID: map[string]entity.BrokerSyncer{
		"2000000001": brokerSyncerReturning(positionOf("GAZP", "BBG004730RP0", "90000000000")),
	}}
	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).
		WithBrokerSyncerSource(syncers).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors, "losing the race is not something the caller has to read about")
	assert.Equal(t, int32(0), resp.Msg.AccountsCreated, "the winner created it, not this sync")
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted, "the winner's account is synced")
	s.AssertNumberOfCalls(t, "CreateAccount", 1)
}

// TestSyncAccount_BrokerRaceLoserFindingNothingSaysSo: the index said the
// account exists and the re-read does not show it — deleted in between. The
// loser names that instead of creating it a third time or syncing nothing
// silently.
func TestSyncAccount_BrokerRaceLoserFindingNothingSaysSo(t *testing.T) {
	parent := tokenAccount()

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(parent, nil)
	s.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{parent}, "", nil)
	s.On("CreateAccount", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("%w: taken", store.ErrAlreadyExists)).Once()

	lister := &mockBrokerLister{refs: []entity.BrokerAccountRef{
		{ID: "2000000001", Name: "Брокерский счёт", Syncable: true, ReadOnly: true},
	}}
	h := newHandler(s).WithMarketDataClient(&mockMDClient{autoAsset: true}).
		WithBrokerSyncerSource(&syncerPerAccount{}).
		WithBrokerAccountListerSource(&mockBrokerListerSource{lister: lister})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	require.NotEmpty(t, resp.Msg.Errors)
	assert.Contains(t, resp.Msg.Errors[0], "created by a concurrent sync and could not be read back")
	s.AssertNumberOfCalls(t, "CreateAccount", 1)
}
