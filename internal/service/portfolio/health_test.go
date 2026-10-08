package portfolio

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

type fakeSourceHealth struct {
	report *entity.PriceSourceReport
	asked  string
}

func (f *fakeSourceHealth) PriceSourceHealth(_ context.Context, userID string) (*entity.PriceSourceReport, error) {
	f.asked = userID
	return f.report, nil
}

func healthAccount(id string, typ entity.AccountType) *entity.Account {
	return &entity.Account{ID: id, UserID: testUserID, Name: id, Type: typ}
}

func byAccount(resp *apiv1.GetAccountHealthResponse) map[string]*apiv1.AccountHealth {
	out := map[string]*apiv1.AccountHealth{}
	for _, a := range resp.Accounts {
		out[a.AccountId] = a
	}
	return out
}

func bySource(resp *apiv1.GetAccountHealthResponse) map[string]*apiv1.SourceHealth {
	out := map[string]*apiv1.SourceHealth{}
	for _, s := range resp.Sources {
		out[s.Provider] = s
	}
	return out
}

// healthFixture is one instance with every way an account goes inert, plus a
// shared source the caller does not own.
func healthFixture(t *testing.T) (*mockStore, *fakeSourceHealth, time.Time) {
	t.Helper()
	until := time.Now().Add(6 * time.Hour).UTC().Truncate(time.Second)
	since := time.Now().Add(-40 * 24 * time.Hour).UTC().Truncate(time.Second)

	store := &mockStore{}
	store.On("ListAccounts", mock.Anything, mock.MatchedBy(func(o ListAccountsOpts) bool {
		return o.UserID == testUserID
	})).Return([]*entity.Account{
		healthAccount("ok", entity.AccountTypeExchange),
		healthAccount("dot", entity.AccountTypeWallet),
		healthAccount("idle", entity.AccountTypeExchange),
		healthAccount("broken", entity.AccountTypeService),
		healthAccount("young", entity.AccountTypeService),
		healthAccount("old", entity.AccountTypeService),
	}, "", nil)
	store.On("ListSyncDeferrals", mock.Anything, testUserID, "").Return([]*entity.SyncDeferral{
		{AccountID: "idle", Misses: 3, NextAttemptAt: until},
	}, nil)
	store.On("ListChainFailures", mock.Anything, "dot").Return([]*entity.ChainFailure{
		{AccountID: "dot", Chain: "hydration", Failures: 384, FailingSince: since, LastError: "subscan API status 404 for hydration"},
	}, nil)

	src := &fakeSourceHealth{report: &entity.PriceSourceReport{
		Sources: []entity.PriceSourceState{
			{Provider: "coingecko", AccountID: "system-cg", Unusable: true, Reason: "paused after repeated refusals", Until: until},
			{Provider: "moex"},
			{Provider: "binance", AccountID: "old"},
		},
		Skipped: []entity.SkippedAccount{
			{Provider: "tinvest", AccountID: "broken", Kind: entity.SkipCannotBuild, Reason: "account unusable: root_ca is required"},
			{Provider: "binance", AccountID: "young", Kind: entity.SkipShadowed, Reason: "another account for this provider is used first: old"},
		},
	}}
	return store, src, until
}

// TestAccountHealthNamesEveryWayAnAccountGoesInert: each form of silence the
// log used to hold arrives as its own reason, and the state is the worst of
// them, decided here rather than by each client.
func TestAccountHealthNamesEveryWayAnAccountGoesInert(t *testing.T) {
	store, src, until := healthFixture(t)
	h := newHandler(store).WithPriceSourceHealth(src)

	resp, err := h.GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)
	got := byAccount(resp.Msg)

	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, got["ok"].State)
	assert.Empty(t, got["ok"].Reasons)
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, got["old"].State, "the account that shadows is the one in use")

	dot := got["dot"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DEGRADED, dot.State)
	require.Len(t, dot.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_CHAIN_FAILING, dot.Reasons[0].Kind)
	assert.Equal(t, "hydration", dot.Reasons[0].GetChain())
	assert.EqualValues(t, 384, dot.Reasons[0].Failures)
	assert.Contains(t, dot.Reasons[0].Message, "384")

	idle := got["idle"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DEGRADED, idle.State)
	require.Len(t, idle.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_SWEEP_DEFERRED, idle.Reasons[0].Kind)
	assert.Equal(t, until, idle.Reasons[0].Until.AsTime())

	broken := got["broken"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, broken.State)
	require.Len(t, broken.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_CANNOT_BUILD, broken.Reasons[0].Kind)

	young := got["young"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, young.State)
	require.Len(t, young.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_SHADOWED, young.Reasons[0].Kind)

	assert.Equal(t, testUserID, src.asked)
}

// TestAccountHealthKeepsUpstreamTextFromNonAdmins: upstream error text is
// somebody else's words and can describe somebody else's credential; only an
// admin gets it, and the server's phrase never carries it.
func TestAccountHealthKeepsUpstreamTextFromNonAdmins(t *testing.T) {
	store, src, _ := healthFixture(t)
	h := newHandler(store).WithPriceSourceHealth(src)

	resp, err := h.GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)
	for _, a := range resp.Msg.Accounts {
		for _, r := range a.Reasons {
			assert.Nil(t, r.Detail, "account %s reason %s", a.AccountId, r.Kind)
			assert.NotContains(t, r.Message, "root_ca")
			assert.NotContains(t, r.Message, "404")
		}
	}
	for _, s := range resp.Msg.Sources {
		for _, r := range s.Reasons {
			assert.Nil(t, r.Detail, "source %s", s.Provider)
		}
	}

	resp, err = h.GetAccountHealth(ctxWithAdmin(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)
	got := byAccount(resp.Msg)
	assert.Contains(t, got["broken"].Reasons[0].GetDetail(), "root_ca is required")
	assert.Contains(t, got["dot"].Reasons[0].GetDetail(), "404")
}

// TestAccountHealthNamesSharedSourcesBySlugOnly: the h8r5 shape. A shared
// CoinGecko pausing after refusals stopped every crypto price, and a user who
// does not own that account must still see it — by slug, never by the account
// behind it.
func TestAccountHealthNamesSharedSourcesBySlugOnly(t *testing.T) {
	store, src, until := healthFixture(t)
	h := newHandler(store).WithPriceSourceHealth(src)

	resp, err := h.GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)
	got := bySource(resp.Msg)

	cg := got["coingecko"]
	require.NotNil(t, cg)
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, cg.State)
	require.Len(t, cg.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_PROVIDER_PAUSED, cg.Reasons[0].Kind)
	assert.Equal(t, until, cg.Reasons[0].Until.AsTime())
	assert.NotContains(t, cg.Reasons[0].Message, "system-cg")

	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, got["moex"].State)
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, got["binance"].State, "a shadowed duplicate does not make its provider unreachable")

	tinvest := got["tinvest"]
	require.NotNil(t, tinvest, "a provider every account of which was passed over is a missing source")
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, tinvest.State)

	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, resp.Msg.SourcesState)
}

// TestAccountHealthPausedSourceMarksTheAccountServingIt: the owner of the
// credential sees the pause on the account itself, with the deadline.
func TestAccountHealthPausedSourceMarksTheAccountServingIt(t *testing.T) {
	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	store := &mockStore{}
	store.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{
		healthAccount("cg", entity.AccountTypeService),
	}, "", nil)
	store.On("ListSyncDeferrals", mock.Anything, testUserID, "").Return(nil, nil)
	src := &fakeSourceHealth{report: &entity.PriceSourceReport{Sources: []entity.PriceSourceState{
		{Provider: "coingecko", AccountID: "cg", Unusable: true, Reason: "this deployment's share of the plan is spent for the period", Until: until},
	}}}

	resp, err := newHandler(store).WithPriceSourceHealth(src).
		GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)

	cg := byAccount(resp.Msg)["cg"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, cg.State)
	require.Len(t, cg.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_PROVIDER_PAUSED, cg.Reasons[0].Kind)
	assert.Equal(t, until, cg.Reasons[0].Until.AsTime())
}

// TestAccountHealthWithoutSourceHealthSaysUnknown: a handler that cannot ask
// the process that prices has nothing to say about sources, and "nothing to
// say" must not arrive as OK.
func TestAccountHealthWithoutSourceHealthSaysUnknown(t *testing.T) {
	store, _, _ := healthFixture(t)

	resp, err := newHandler(store).GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNKNOWN, resp.Msg.SourcesState)
	assert.Empty(t, resp.Msg.Sources)
	// What the store knows is still reported.
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DEGRADED, byAccount(resp.Msg)["dot"].State)
}

// TestAccountHealthOfAnotherUsersAccountIsNotFound: a named account goes
// through the same ownership check as GetAccount.
func TestAccountHealthOfAnotherUsersAccountIsNotFound(t *testing.T) {
	store := &mockStore{}
	store.On("GetAccount", mock.Anything, "theirs").Return(&entity.Account{ID: "theirs", UserID: testUserID2}, nil)

	_, err := newHandler(store).GetAccountHealth(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.GetAccountHealthRequest{AccountId: proto.String("theirs")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestAccountHealthOfANamedAccountReadsItsOwner: an admin naming somebody
// else's account gets that owner's deferrals and price registry, not their own.
func TestAccountHealthOfANamedAccountReadsItsOwner(t *testing.T) {
	store := &mockStore{}
	store.On("GetAccount", mock.Anything, "theirs").Return(&entity.Account{ID: "theirs", UserID: testUserID2, Type: entity.AccountTypeExchange}, nil)
	store.On("ListSyncDeferrals", mock.Anything, testUserID2, "theirs").Return(nil, nil)
	src := &fakeSourceHealth{report: &entity.PriceSourceReport{}}

	resp, err := newHandler(store).WithPriceSourceHealth(src).GetAccountHealth(ctxWithAdmin(testUserID),
		connect.NewRequest(&apiv1.GetAccountHealthRequest{AccountId: proto.String("theirs")}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Accounts, 1)
	assert.Equal(t, testUserID2, src.asked)
	store.AssertExpectations(t)
}

// TestAccountHealthPriceProblemDoesNotKillASyncingAccount: an exchange account
// that also declares market data, for a provider with no price adapter, still
// syncs balances. It is DEGRADED, and the provider is not a missing price
// source — it never was one.
func TestAccountHealthPriceProblemDoesNotKillASyncingAccount(t *testing.T) {
	store := &mockStore{}
	gate := healthAccount("gate", entity.AccountTypeExchange)
	gate.Capabilities = []entity.AccountCapability{entity.CapabilityPortfolioSync, entity.CapabilityMarketData}
	store.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{gate}, "", nil)
	store.On("ListSyncDeferrals", mock.Anything, testUserID, "").Return(nil, nil)
	src := &fakeSourceHealth{report: &entity.PriceSourceReport{
		Sources: []entity.PriceSourceState{{Provider: "moex"}},
		Skipped: []entity.SkippedAccount{{Provider: "gateio", AccountID: "gate", Kind: entity.SkipNoAdapter}},
	}}

	resp, err := newHandler(store).WithPriceSourceHealth(src).
		GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)

	got := byAccount(resp.Msg)["gate"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DEGRADED, got.State)
	require.Len(t, got.Reasons, 1)
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_NO_ADAPTER, got.Reasons[0].Kind)

	assert.NotContains(t, bySource(resp.Msg), "gateio")
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, resp.Msg.SourcesState)
}

// TestAccountHealthDisabledIsTheOwnersChoiceNotAFault: a stood-down account
// reports DISABLED and nothing else — a pending deferral or a failing chain
// on it describes work its owner turned off — and a source no account serves
// because one was disabled is DISABLED too, without colouring the report.
func TestAccountHealthDisabledIsTheOwnersChoiceNotAFault(t *testing.T) {
	since := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
	off := healthAccount("off", entity.AccountTypeWallet)
	off.DisabledAt = &since

	store := &mockStore{}
	store.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account{
		healthAccount("ok", entity.AccountTypeExchange), off,
	}, "", nil)
	store.On("ListSyncDeferrals", mock.Anything, testUserID, "").Return([]*entity.SyncDeferral{
		{AccountID: "off", Misses: 2, NextAttemptAt: time.Now().Add(time.Hour)},
	}, nil)
	src := &fakeSourceHealth{report: &entity.PriceSourceReport{
		Sources: []entity.PriceSourceState{{Provider: "moex"}},
		Skipped: []entity.SkippedAccount{
			{Provider: "alchemy", AccountID: "off", Kind: entity.SkipDisabled, Reason: "disabled by its owner"},
			{Provider: "alchemy", AccountID: "off-2", Kind: entity.SkipDisabled, Reason: "disabled by its owner"},
			{Provider: "tinvest", AccountID: "t1", Kind: entity.SkipDisabled, Reason: "disabled by its owner"},
			{Provider: "tinvest", AccountID: "t2", Kind: entity.SkipCannotBuild, Reason: "account unusable: root_ca is required"},
		},
	}}
	h := newHandler(store).WithPriceSourceHealth(src)

	resp, err := h.GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)

	got := byAccount(resp.Msg)["off"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DISABLED, got.State)
	require.Len(t, got.Reasons, 1, "no deferral or price reason on a disabled account")
	assert.Equal(t, apiv1.HealthReasonKind_HEALTH_REASON_KIND_DISABLED, got.Reasons[0].Kind)
	assert.Equal(t, since, got.Reasons[0].Since.AsTime())
	store.AssertNotCalled(t, "ListChainFailures", mock.Anything, "off")

	sources := bySource(resp.Msg)
	require.NotNil(t, sources["alchemy"])
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DISABLED, sources["alchemy"].State)
	assert.Len(t, sources["alchemy"].Reasons, 1, "the disabled phrase is about the provider, said once")
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, sources["tinvest"].State,
		"a broken account beside a disabled one still reads as broken")

	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK,
		worse(apiv1.HealthState_HEALTH_STATE_OK, apiv1.HealthState_HEALTH_STATE_DISABLED),
		"a disabled source ranks below OK when states combine")
}
