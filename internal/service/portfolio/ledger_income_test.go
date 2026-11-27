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

// incomeSyncer is a broker syncer that also reads income. Its positions are
// fixed; its income answer is whatever the test sets.
type incomeSyncer struct {
	positions []entity.BrokerPosition
	income    []entity.BrokerIncome
	err       error
	froms     []time.Time
}

func (s *incomeSyncer) SyncBroker(context.Context) ([]entity.BrokerPosition, entity.BrokerSkips, error) {
	return s.positions, entity.BrokerSkips{}, nil
}

func (s *incomeSyncer) ReadIncome(_ context.Context, from, _ time.Time) ([]entity.BrokerIncome, error) {
	s.froms = append(s.froms, from)
	return s.income, s.err
}

func payment(id, kind, amount, currency string, at time.Time) entity.BrokerIncome {
	return entity.BrokerIncome{ID: id, At: at, Kind: entity.IncomeKind(kind), Currency: currency,
		Amount: decimal.RequireFromString(amount), Raw: []byte(`{}`)}
}

func incomeHandler(s Store, syncer entity.BrokerSyncer) *Handler {
	return newHandler(s).
		WithMarketDataClient(&mockMDClient{autoAsset: true}).
		WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})
}

// TestReadBrokerIncome_WritesEachPaymentOnceAndMovesTheCursor: the first run
// reads the whole history; a coupon is an in leg, its tax an out leg of the
// same kind at the exact amount; a second run writes nothing and starts a
// window before the first one ended.
func TestReadBrokerIncome_WritesEachPaymentOnceAndMovesTheCursor(t *testing.T) {
	at := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	syncer := &incomeSyncer{income: []entity.BrokerIncome{
		payment("op-1", "coupon", "1000", "RUB", at),
		payment("op-2", "coupon", "-130.5", "RUB", at.Add(time.Second)),
		payment("op-3", "dividend", "2.75", "USD", at),
	}}
	s := newLedgerStore()
	h := incomeHandler(s, syncer)
	acct := brokerAccount()

	before := time.Now()
	out := h.readBrokerIncome(context.Background(), acct)
	require.Empty(t, out.errors)
	assert.Equal(t, int32(3), out.eventsCreated)
	assert.True(t, syncer.froms[0].IsZero(), "the first run reads the whole history")

	tax := s.events["sync:tinvest|op-2"]
	require.NotNil(t, tax)
	assert.Equal(t, entity.LedgerKindIncome, tax.Kind)
	assert.Equal(t, entity.IncomeCoupon, tax.IncomeKind)
	require.Len(t, tax.Legs, 1)
	assert.Equal(t, entity.LedgerOut, tax.Legs[0].Direction)
	assert.Equal(t, entity.LedgerRoleIncome, tax.Legs[0].Role)
	assert.Equal(t, "130500000000", tax.Legs[0].Amount.String())
	assert.Equal(t, uint32(9), tax.Legs[0].Decimals)
	assert.Equal(t, "asset-RUB", tax.Legs[0].AssetID)
	assert.Equal(t, entity.LedgerIn, s.events["sync:tinvest|op-1"].Legs[0].Direction)
	assert.Equal(t, "asset-USD", s.events["sync:tinvest|op-3"].Legs[0].AssetID)

	cursor := s.cursors[""]
	require.NotNil(t, cursor)
	assert.Equal(t, "sync:tinvest", cursor.Source)
	assert.Empty(t, cursor.LastError)
	assert.False(t, cursor.AdvancedAt.Before(before))

	again := h.readBrokerIncome(context.Background(), acct)
	require.Empty(t, again.errors)
	assert.Zero(t, again.eventsCreated, "a second run writes nothing")
	assert.Len(t, s.events, 3)
	assert.True(t, syncer.froms[1].Equal(cursor.AdvancedAt.Add(-incomeRereadWindow)))
}

// TestReadBrokerIncome_FailureKeepsTheCursor: a failed read writes nothing,
// leaves the cursor where it was and stores why.
func TestReadBrokerIncome_FailureKeepsTheCursor(t *testing.T) {
	advanced := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	s := newLedgerStore()
	s.cursors[""] = &entity.LedgerCursor{AccountID: testAccountID, Source: "sync:tinvest", AdvancedAt: advanced, CheckedAt: advanced}
	syncer := &incomeSyncer{err: errors.New("tinvest: status 500")}

	out := incomeHandler(s, syncer).readBrokerIncome(context.Background(), brokerAccount())
	require.Len(t, out.errors, 1)
	assert.Contains(t, out.errors[0], "status 500")
	assert.Zero(t, out.eventsCreated)

	cursor := s.cursors[""]
	assert.True(t, advanced.Equal(cursor.AdvancedAt), "the cursor does not move")
	assert.Contains(t, cursor.LastError, "status 500")
	assert.True(t, cursor.CheckedAt.After(advanced))
}

// TestReadBrokerIncome_AnotherSourceDoesNotContinue: refs from another
// provider would not deduplicate against what is recorded.
func TestReadBrokerIncome_AnotherSourceDoesNotContinue(t *testing.T) {
	s := newLedgerStore()
	s.cursors[""] = &entity.LedgerCursor{AccountID: testAccountID, Source: "sync:otherbroker"}
	syncer := &incomeSyncer{income: []entity.BrokerIncome{payment("op-1", "coupon", "1", "RUB", time.Now())}}

	out := incomeHandler(s, syncer).readBrokerIncome(context.Background(), brokerAccount())
	require.Len(t, out.errors, 1)
	assert.Empty(t, s.events)
	assert.Empty(t, syncer.froms, "nothing is read")
	assert.Equal(t, "sync:otherbroker", s.cursors[""].Source)
}

// TestSyncAccount_IncomeFailureDoesNotFailTheBalanceSync: the balances are
// written, the failure is in ledger_errors only, and account health then
// reports it.
func TestSyncAccount_IncomeFailureDoesNotFailTheBalanceSync(t *testing.T) {
	acct := brokerAccount()
	s := newLedgerStore()
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil).Once()
	s.On("RecordSyncAnswer", mock.Anything, testAccountID, mock.Anything).Return(nil)
	syncer := &incomeSyncer{
		positions: []entity.BrokerPosition{{Symbol: "RUB", Name: "RUB", Type: entity.AssetTypeForex, Market: "forex",
			Currency: "rub", Amount: "1000000000", Decimals: 9, Liquidity: entity.LiquidityLiquid}},
		err: errors.New("tinvest: status 503"),
	}
	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)
	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors, "the balances are complete without the history")
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	require.Len(t, resp.Msg.LedgerErrors, 1)
	assert.Contains(t, resp.Msg.LedgerErrors[0], "status 503")
	assert.True(t, s.cursors[""].AdvancedAt.IsZero(), "a cursor that never read stays unread")

	reason := ledgerReasonOf(t, h, s)
	require.NotNil(t, reason, "account health reports the failed history")
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DEGRADED, stateOf(reason))
	assert.Nil(t, reason.Since, "it was never read in full")
	assert.Nil(t, reason.Detail, "upstream text is for admins")
	s.AssertExpectations(t)
}

func ledgerReasonOf(t *testing.T, h *Handler, s *ledgerStore) *apiv1.HealthReason {
	t.Helper()
	s.On("ListSyncDeferrals", mock.Anything, testUserID, testAccountID).Return([]*entity.SyncDeferral{}, nil)
	resp, err := h.GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{AccountId: new(testAccountID)}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Accounts, 1)
	for _, r := range resp.Msg.Accounts[0].Reasons {
		if r.Kind == apiv1.HealthReasonKind_HEALTH_REASON_KIND_LEDGER_FAILING {
			return r
		}
	}
	return nil
}

// TestLedgerReason_AdminSeesTheDetailAndTheLastFullRead.
func TestLedgerReason_AdminSeesTheDetailAndTheLastFullRead(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r := ledgerReason(&entity.LedgerCursor{Source: "sync:tinvest", LastError: "boom", AdvancedAt: at}, true)
	assert.Contains(t, r.Message, "tinvest")
	require.NotNil(t, r.Detail)
	assert.Equal(t, "boom", *r.Detail)
	assert.True(t, at.Equal(r.Since.AsTime()))
}

// TestReadBrokerIncome_RenamedOperationIsNotWrittenTwice: the broker says an
// operation id may change. The same payment under a new id is recognised by
// its content and not written again; two papers paying the same sum at the
// same moment are still two payments.
func TestReadBrokerIncome_RenamedOperationIsNotWrittenTwice(t *testing.T) {
	// Inside the window the second run re-reads.
	at := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second).Add(123456789)
	coupon := func(id, paper string) entity.BrokerIncome {
		p := payment(id, "coupon", "36.4", "RUB", at)
		p.Instrument = paper
		return p
	}
	syncer := &incomeSyncer{income: []entity.BrokerIncome{coupon("op-1", "ofz-a"), coupon("op-2", "ofz-b")}}
	s := newLedgerStore()
	h := incomeHandler(s, syncer)

	first := h.readBrokerIncome(context.Background(), brokerAccount())
	require.Empty(t, first.errors)
	assert.Equal(t, int32(2), first.eventsCreated, "two papers, two payments")

	syncer.income = []entity.BrokerIncome{coupon("op-1-renamed", "ofz-a"), coupon("op-2", "ofz-b"), coupon("op-3", "ofz-c")}
	again := h.readBrokerIncome(context.Background(), brokerAccount())
	require.Empty(t, again.errors)
	assert.Equal(t, int32(1), again.eventsCreated, "only the new paper's payment is written")
	assert.Len(t, s.events, 3)
	assert.Nil(t, s.events["sync:tinvest|op-1-renamed"])
}

func TestIncomeKey_IsWhatThePaymentIs(t *testing.T) {
	at := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	event := func(op entity.BrokerIncome) *entity.LedgerEvent {
		e, err := brokerIncomeEvent(testAccountID, "sync:tinvest", "asset-RUB", op)
		require.NoError(t, err)
		return e
	}
	base := payment("a", "coupon", "1", "RUB", at)
	key := incomeKey(event(base), "p")
	renamed := base
	renamed.ID = "b"
	assert.Equal(t, key, incomeKey(event(renamed), "p"), "the id is not part of it")
	for name, other := range map[string]entity.BrokerIncome{
		"amount":    payment("a", "coupon", "2", "RUB", at),
		"direction": payment("a", "coupon", "-1", "RUB", at),
		"kind":      payment("a", "dividend", "1", "RUB", at),
		"moment":    payment("a", "coupon", "1", "RUB", at.Add(time.Microsecond)),
	} {
		assert.NotEqual(t, key, incomeKey(event(other), "p"), name)
	}
	assert.NotEqual(t, key, incomeKey(event(base), "q"), "paper")
}

// TestReadBrokerIncome_SameContentIsAMultiset: the content match only stands
// in for an id the broker renamed. Two operations with distinct ids and the
// same content (overnight interest names no paper) are two payments, in one
// read and after it; a renamed one still matches a recorded one, once.
func TestReadBrokerIncome_SameContentIsAMultiset(t *testing.T) {
	at := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	overnight := func(id string) entity.BrokerIncome { return payment(id, "interest", "0.42", "RUB", at) }
	syncer := &incomeSyncer{income: []entity.BrokerIncome{overnight("op-a"), overnight("op-b")}}
	s := newLedgerStore()
	h := incomeHandler(s, syncer)

	first := h.readBrokerIncome(context.Background(), brokerAccount())
	require.Empty(t, first.errors)
	assert.Equal(t, int32(2), first.eventsCreated, "two distinct operations in one read are two events")

	again := h.readBrokerIncome(context.Background(), brokerAccount())
	require.Empty(t, again.errors)
	assert.Zero(t, again.eventsCreated, "a resync writes no row")

	syncer.income = []entity.BrokerIncome{overnight("op-a-renamed"), overnight("op-b")}
	renamed := h.readBrokerIncome(context.Background(), brokerAccount())
	require.Empty(t, renamed.errors)
	assert.Zero(t, renamed.eventsCreated, "a renamed operation matches the recorded one it replaced")

	syncer.income = []entity.BrokerIncome{overnight("op-a-renamed"), overnight("op-b"), overnight("op-c")}
	third := h.readBrokerIncome(context.Background(), brokerAccount())
	require.Empty(t, third.errors)
	assert.Equal(t, int32(1), third.eventsCreated, "a third operation of the same content has nothing left to match")
	assert.Len(t, s.events, 3)
	assert.NotNil(t, s.events["sync:tinvest|op-c"])
}
