package automation

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/execution"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// waitingVenue proposes every trade for a signature and answers checks with
// whatever the test sets next.
type waitingVenue struct {
	mu        sync.Mutex
	submitted []execution.Key
	checked   []execution.Key
	next      execution.Outcome
}

func (v *waitingVenue) Submit(_ context.Context, _ execution.Account, _ execution.Trade, key execution.Key) (execution.Outcome, error) {
	v.mu.Lock()
	v.submitted = append(v.submitted, key)
	v.mu.Unlock()
	return execution.Outcome{Status: execution.OutcomePendingSignature, VenueRef: "safetx-" + key.String()}, nil
}

func (v *waitingVenue) Check(_ context.Context, _ execution.Account, key execution.Key, _ execution.TradeResult) (execution.Outcome, error) {
	v.checked = append(v.checked, key)
	return v.next, nil
}

var walletPolicy = map[string]string{
	execution.PolicyMaxTradeAmount: "100",
	execution.PolicyAllowedAssets:  "base,quote",
}

// walletHandler wires the fake engine to a wallet account carrying data,
// with venue registered for wallets.
func walletHandler(s Store, venue execution.Venue, data map[string]string) *Handler {
	return newHandler(s).
		WithEngines(execution.NewEngines(&fakeEngine{})).
		WithVenues(execution.Venues{apiv1.AccountType_ACCOUNT_TYPE_WALLET: venue}).
		WithPortfolioClient(&fakePortfolio{accountType: apiv1.AccountType_ACCOUNT_TYPE_WALLET, portfolioID: "p-1", data: data}).
		WithMarketDataClient(&fakeMD{price: &apiv1.Price{Last: "5000", Decimals: 2, Timestamp: timestamppb.New(time.Now())}})
}

// waitingExecution is a stored execution of r-1 whose one trade is in state.
func waitingExecution(t *testing.T, id string, state execution.TradeState) *entity.RuleExecution {
	t.Helper()
	summary, err := execution.Summary([]execution.TradeResult{{
		Trade: execution.Trade{Side: execution.SideBuy, AssetID: "base", QuoteAssetID: "quote",
			QuoteAmount: decimal.NewFromInt(100), Price: decimal.NewFromInt(50), PriceAsOf: time.Now()},
		State:    state,
		VenueRef: "safetx-" + id + ":0",
	}})
	require.NoError(t, err)
	return &entity.RuleExecution{ID: id, RuleID: "r-1", UserID: "user-1", Status: entity.ExecutionStatusPending, ExecutionSummary: summary}
}

func waitingOf(rule string) any {
	return mock.MatchedBy(func(o ListRuleExecutionsOpts) bool {
		return o.Status == entity.ExecutionStatusPending && o.RuleID == rule
	})
}

func TestExecuteRule_WaitingVenueLeavesExecutionPending(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	final := expectRun(s)
	venue := &waitingVenue{}
	h := walletHandler(s, venue, walletPolicy)

	resp, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.Equal(t, apiv1.ExecutionStatus_EXECUTION_STATUS_PENDING, resp.Msg.Execution.Status)
	assert.Nil(t, final.CompletedAt, "a waiting execution is not complete")
	assert.Empty(t, final.CreatedTransactionIDs, "a chain account is not journaled")
	results, err := execution.ParseSummary(final.ExecutionSummary)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, execution.StatePendingSignature, results[0].State)
	assert.Equal(t, "safetx-"+testExecID+":0", results[0].VenueRef, "the venue's reference is stored for the next check")
}

// TestExecuteRule_NextRunExpiresUnsignedProposal: one waiting proposal per
// rule — the next run expires the unsigned one and proposes again.
func TestExecuteRule_NextRunExpiresUnsignedProposal(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	old := waitingExecution(t, "e-old", execution.StatePendingSignature)
	s.On("ListRuleExecutions", mock.Anything, waitingOf("r-1")).Return([]*entity.RuleExecution{old}, "", nil).Once()
	var expired *entity.RuleExecution
	s.On("UpdateRuleExecution", mock.Anything, mock.MatchedBy(func(e *entity.RuleExecution) bool { return e.ID == "e-old" }), mock.Anything).
		Run(func(args mock.Arguments) { c := *args.Get(1).(*entity.RuleExecution); expired = &c }).
		Return(old, nil).Once()
	final := expectRun(s)
	venue := &waitingVenue{}
	h := walletHandler(s, venue, walletPolicy)

	_, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)

	require.NotNil(t, expired)
	assert.Equal(t, entity.ExecutionStatusCancelled, expired.Status)
	assert.Contains(t, expired.ErrorMessage, expiredBySuccessor)
	assert.NotNil(t, expired.CompletedAt)
	assert.Len(t, venue.submitted, 1, "and a fresh proposal is made")
	assert.Equal(t, entity.ExecutionStatusPending, final.Status)
}

// TestExecuteRule_SubmittedTransactionHoldsTheNextRun: a signed transaction
// on its way to a block cannot be withdrawn, so no second one is proposed.
func TestExecuteRule_SubmittedTransactionHoldsTheNextRun(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	s.On("ListRuleExecutions", mock.Anything, waitingOf("r-1")).
		Return([]*entity.RuleExecution{waitingExecution(t, "e-old", execution.StateSubmitted)}, "", nil).Once()
	var recorded *entity.RuleExecution
	s.On("CreateRuleExecution", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { recorded = args.Get(1).(*entity.RuleExecution) }).
		Return(&entity.RuleExecution{ID: "e-new", RuleID: "r-1", Status: entity.ExecutionStatusFailed}, nil).Once()
	venue := &waitingVenue{}
	h := walletHandler(s, venue, walletPolicy)

	_, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.Empty(t, venue.submitted)
	require.NotNil(t, recorded)
	assert.Equal(t, entity.ExecutionStatusFailed, recorded.Status)
	assert.Contains(t, recorded.ErrorMessage, "waiting for its block")
	s.AssertNotCalled(t, "UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything)
}

// TestExecuteRule_PolicyRefusesBeforeTheVenue: a real account without a
// policy, or a trade above it, is rejected with the reason and the venue is
// never asked.
func TestExecuteRule_PolicyRefusesBeforeTheVenue(t *testing.T) {
	for name, data := range map[string]map[string]string{
		"no policy":       nil,
		"over the limit":  {execution.PolicyMaxTradeAmount: "99", execution.PolicyAllowedAssets: "base,quote"},
		"asset not on it": {execution.PolicyMaxTradeAmount: "100", execution.PolicyAllowedAssets: "quote"},
	} {
		t.Run(name, func(t *testing.T) {
			s := &mockStore{}
			s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
			final := expectRun(s)
			venue := &waitingVenue{}
			h := walletHandler(s, venue, data)

			_, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
			require.NoError(t, err)
			assert.Empty(t, venue.submitted)
			assert.Equal(t, entity.ExecutionStatusFailed, final.Status)
			assert.Contains(t, final.ErrorMessage, "policy")
		})
	}
}

func TestConfirmWaiting_MovesTradesOn(t *testing.T) {
	s := &mockStore{}
	stored := waitingExecution(t, "e-1", execution.StateSubmitted)
	s.On("ListRuleExecutions", mock.Anything, waitingOf("")).Return([]*entity.RuleExecution{stored}, "", nil).Once()
	s.On("GetRuleExecution", mock.Anything, "e-1").Return(stored, nil)
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	var written *entity.RuleExecution
	s.On("UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { c := *args.Get(1).(*entity.RuleExecution); written = &c }).
		Return(&entity.RuleExecution{}, nil).Once()
	venue := &waitingVenue{next: execution.Outcome{Status: execution.OutcomeFilled, TxHash: "0xtx",
		Fill: execution.Fill{BaseAmount: decimal.RequireFromString("0.02")}}}
	h := walletHandler(s, venue, walletPolicy)

	// The scheduler calls with no user: each execution is checked as its owner.
	moved, err := h.ConfirmWaiting(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, moved)
	assert.Equal(t, []execution.Key{{ExecutionID: "e-1", Index: 0}}, venue.checked)
	require.NotNil(t, written)
	assert.Equal(t, entity.ExecutionStatusCompleted, written.Status)
	assert.NotNil(t, written.CompletedAt)
	results, err := execution.ParseSummary(written.ExecutionSummary)
	require.NoError(t, err)
	assert.Equal(t, "0xtx", results[0].TxHash)
}

func TestConfirmWaiting_StillWaitingWritesNothing(t *testing.T) {
	s := &mockStore{}
	stored := waitingExecution(t, "e-1", execution.StatePendingSignature)
	s.On("ListRuleExecutions", mock.Anything, waitingOf("")).Return([]*entity.RuleExecution{stored}, "", nil).Once()
	s.On("GetRuleExecution", mock.Anything, "e-1").Return(stored, nil)
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	venue := &waitingVenue{next: execution.Outcome{Status: execution.OutcomePendingSignature}}
	h := walletHandler(s, venue, walletPolicy)

	moved, err := h.ConfirmWaiting(context.Background())
	require.NoError(t, err)
	assert.Zero(t, moved)
	s.AssertNotCalled(t, "UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything)
}

func TestCancelRuleExecution_ExpiresProposalButNotASubmittedTx(t *testing.T) {
	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})

	s := &mockStore{}
	s.On("GetRuleExecution", mock.Anything, "e-1").Return(waitingExecution(t, "e-1", execution.StatePendingSignature), nil)
	s.On("GetRule", mock.Anything, "r-1").Return(testRule("r-1"), nil)
	var written *entity.RuleExecution
	s.On("UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { c := *args.Get(1).(*entity.RuleExecution); written = &c }).
		Return(&entity.RuleExecution{}, nil)
	_, err := newHandler(s).CancelRuleExecution(ctx, connect.NewRequest(&apiv1.CancelRuleExecutionRequest{ExecutionId: "e-1"}))
	require.NoError(t, err)
	require.NotNil(t, written)
	assert.Equal(t, entity.ExecutionStatusCancelled, written.Status)
	results, err := execution.ParseSummary(written.ExecutionSummary)
	require.NoError(t, err)
	assert.Equal(t, execution.StateExpired, results[0].State)

	s2 := &mockStore{}
	s2.On("GetRuleExecution", mock.Anything, "e-2").Return(waitingExecution(t, "e-2", execution.StateSubmitted), nil)
	s2.On("GetRule", mock.Anything, "r-1").Return(testRule("r-1"), nil)
	_, err = newHandler(s2).CancelRuleExecution(ctx, connect.NewRequest(&apiv1.CancelRuleExecutionRequest{ExecutionId: "e-2"}))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	s2.AssertNotCalled(t, "UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything)
}

// TestConfirmWaiting_LeavesAnExecutionMovedSinceTheList: a cancel or the next
// run may move an execution between the list and its check; the pass re-reads
// it under the rule's lock and keeps their state.
func TestConfirmWaiting_LeavesAnExecutionMovedSinceTheList(t *testing.T) {
	s := &mockStore{}
	listed := waitingExecution(t, "e-1", execution.StatePendingSignature)
	cancelled := *listed
	cancelled.Status = entity.ExecutionStatusCancelled
	s.On("ListRuleExecutions", mock.Anything, waitingOf("")).Return([]*entity.RuleExecution{listed}, "", nil).Once()
	s.On("GetRuleExecution", mock.Anything, "e-1").Return(&cancelled, nil)
	venue := &waitingVenue{next: execution.Outcome{Status: execution.OutcomeSubmitted}}
	h := walletHandler(s, venue, walletPolicy)

	moved, err := h.ConfirmWaiting(context.Background())
	require.NoError(t, err)
	assert.Zero(t, moved)
	assert.Empty(t, venue.checked, "the venue is not asked about an execution that stopped waiting")
	s.AssertNotCalled(t, "UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything)
}

// memExecutions keeps executions in memory, enough for concurrent runs of one
// rule to see each other's results; everything else goes to the mock.
type memExecutions struct {
	*mockStore
	mu    sync.Mutex
	execs []*entity.RuleExecution
}

func (m *memExecutions) CreateRuleExecution(_ context.Context, e *entity.RuleExecution) (*entity.RuleExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *e
	c.ID = fmt.Sprintf("e-%d", len(m.execs)+1)
	m.execs = append(m.execs, &c)
	out := c
	return &out, nil
}

func (m *memExecutions) UpdateRuleExecution(_ context.Context, e *entity.RuleExecution, _ []string) (*entity.RuleExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.execs {
		if x.ID == e.ID {
			c := *e
			m.execs[i] = &c
		}
	}
	out := *e
	return &out, nil
}

func (m *memExecutions) ListRuleExecutions(_ context.Context, o ListRuleExecutionsOpts) ([]*entity.RuleExecution, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*entity.RuleExecution
	for _, x := range m.execs {
		if x.Status == o.Status && (o.RuleID == "" || x.RuleID == o.RuleID) {
			c := *x
			out = append(out, &c)
		}
	}
	return out, "", nil
}

func (m *memExecutions) count(status entity.ExecutionStatus) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.execs {
		if x.Status == status {
			n++
		}
	}
	return n
}

// TestExecuteRule_ConcurrentRunsLeaveOneProposal: a scheduler fire and a
// manual run of one rule at once must not both find no waiting proposal and
// both propose.
func TestExecuteRule_ConcurrentRunsLeaveOneProposal(t *testing.T) {
	mem := &memExecutions{mockStore: &mockStore{}}
	mem.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	h := walletHandler(mem, &waitingVenue{}, walletPolicy)

	const runs = 8
	var wg sync.WaitGroup
	for range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, mem.count(entity.ExecutionStatusPending), "one waiting proposal per rule")
	assert.Equal(t, runs-1, mem.count(entity.ExecutionStatusCancelled), "every earlier one expired")
}
