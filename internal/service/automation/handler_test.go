package automation

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mock Store ---

type mockStore struct {
	mock.Mock
}

func (m *mockStore) CreateRule(ctx context.Context, r *entity.Rule) (*entity.Rule, error) {
	args := m.Called(ctx, r)
	if v := args.Get(0); v != nil {
		return v.(*entity.Rule), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetRule(ctx context.Context, id string) (*entity.Rule, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.Rule), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdateRule(ctx context.Context, r *entity.Rule, fields []string) (*entity.Rule, error) {
	args := m.Called(ctx, r, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.Rule), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) DeleteRule(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) ListRules(ctx context.Context, opts ListRulesOpts) ([]*entity.Rule, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Rule), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) CreateRuleExecution(ctx context.Context, e *entity.RuleExecution) (*entity.RuleExecution, error) {
	args := m.Called(ctx, e)
	if v := args.Get(0); v != nil {
		return v.(*entity.RuleExecution), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetRuleExecution(ctx context.Context, id string) (*entity.RuleExecution, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.RuleExecution), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdateRuleExecution(ctx context.Context, e *entity.RuleExecution, fields []string) (*entity.RuleExecution, error) {
	args := m.Called(ctx, e, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.RuleExecution), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) ListRuleExecutions(ctx context.Context, opts ListRuleExecutionsOpts) ([]*entity.RuleExecution, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.RuleExecution), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

// --- Helpers ---

func newHandler(s Store) *Handler {
	return NewHandler(s, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func testRule(id string) *entity.Rule {
	return &entity.Rule{
		ID:          id,
		Name:        "DCA Rule",
		RuleType:    "dca",
		PortfolioID: "p-1",
		UserID:      "user-1",
		Status:      entity.RuleStatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func testExecution(id, ruleID string) *entity.RuleExecution {
	return &entity.RuleExecution{
		ID:        id,
		RuleID:    ruleID,
		Status:    entity.ExecutionStatusPending,
		StartedAt: time.Now(),
	}
}

// --- Tests: Rule CRUD ---

func TestCreateRule_MissingRule(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateRule(context.Background(), connect.NewRequest(&apiv1.CreateRuleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateRule_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreateRule", mock.Anything, mock.Anything).Return(testRule("r-1"), nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.CreateRule(ctx, connect.NewRequest(&apiv1.CreateRuleRequest{
		Rule: &apiv1.Rule{Name: "DCA Rule", RuleType: "dca", PortfolioId: "p-1"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "r-1", resp.Msg.Id)
	assert.Equal(t, "DCA Rule", resp.Msg.Name)
}

func TestGetRule_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.GetRule(context.Background(), connect.NewRequest(&apiv1.GetRuleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetRule_NotFound(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-x").Return(nil, store.ErrNotFound)
	h := newHandler(s)

	_, err := h.GetRule(context.Background(), connect.NewRequest(&apiv1.GetRuleRequest{Id: "r-x"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetRule_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(testRule("r-1"), nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.GetRule(ctx, connect.NewRequest(&apiv1.GetRuleRequest{Id: "r-1"}))
	require.NoError(t, err)
	assert.Equal(t, "r-1", resp.Msg.Id)
}

func TestDeleteRule_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.DeleteRule(context.Background(), connect.NewRequest(&apiv1.DeleteRuleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDeleteRule_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(testRule("r-1"), nil)
	s.On("DeleteRule", mock.Anything, "r-1").Return(nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	_, err := h.DeleteRule(ctx, connect.NewRequest(&apiv1.DeleteRuleRequest{Id: "r-1"}))
	require.NoError(t, err)
}

func TestListRules_WithFilters(t *testing.T) {
	s := &mockStore{}
	portfolioID := "p-1"
	s.On("ListRules", mock.Anything, ListRulesOpts{UserID: "user-1", PortfolioID: "p-1"}).
		Return([]*entity.Rule{testRule("r-1")}, "", nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.ListRules(ctx, connect.NewRequest(&apiv1.ListRulesRequest{
		PortfolioId: &portfolioID,
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Rules, 1)
}

// --- Tests: Rule status management ---

func TestEnableRule_OK(t *testing.T) {
	s := &mockStore{}
	r := testRule("r-1")
	r.Status = entity.RuleStatusDisabled
	enabled := testRule("r-1")
	enabled.Status = entity.RuleStatusActive
	s.On("GetRule", mock.Anything, "r-1").Return(r, nil)
	s.On("UpdateRule", mock.Anything, mock.MatchedBy(func(r *entity.Rule) bool {
		return r.Status == entity.RuleStatusActive
	}), []string{"status"}).Return(enabled, nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.EnableRule(ctx, connect.NewRequest(&apiv1.EnableRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.Equal(t, apiv1.RuleStatus_RULE_STATUS_ACTIVE, resp.Msg.Status)
}

func TestDisableRule_OK(t *testing.T) {
	s := &mockStore{}
	r := testRule("r-1")
	disabled := testRule("r-1")
	disabled.Status = entity.RuleStatusDisabled
	s.On("GetRule", mock.Anything, "r-1").Return(r, nil)
	s.On("UpdateRule", mock.Anything, mock.MatchedBy(func(r *entity.Rule) bool {
		return r.Status == entity.RuleStatusDisabled
	}), []string{"status"}).Return(disabled, nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.DisableRule(ctx, connect.NewRequest(&apiv1.DisableRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.Equal(t, apiv1.RuleStatus_RULE_STATUS_DISABLED, resp.Msg.Status)
}

func TestPauseRule_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.PauseRule(context.Background(), connect.NewRequest(&apiv1.PauseRuleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestResumeRule_NotFound(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-x").Return(nil, store.ErrNotFound)
	h := newHandler(s)

	_, err := h.ResumeRule(context.Background(), connect.NewRequest(&apiv1.ResumeRuleRequest{RuleId: "r-x"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// --- Tests: ValidateRule ---

func TestValidateRule_MissingRule(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.ValidateRule(context.Background(), connect.NewRequest(&apiv1.ValidateRuleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestValidateRule_Invalid(t *testing.T) {
	h := newHandler(&mockStore{})
	resp, err := h.ValidateRule(context.Background(), connect.NewRequest(&apiv1.ValidateRuleRequest{
		Rule: &apiv1.Rule{}, // missing name, rule_type, portfolio_id
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Valid)
	assert.Len(t, resp.Msg.ValidationErrors, 3)
}

func TestValidateRule_Valid(t *testing.T) {
	h := newHandler(&mockStore{})
	resp, err := h.ValidateRule(context.Background(), connect.NewRequest(&apiv1.ValidateRuleRequest{
		Rule: &apiv1.Rule{Name: "DCA", RuleType: "dca", PortfolioId: "p-1"},
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Valid)
	assert.Empty(t, resp.Msg.ValidationErrors)
}

// --- Tests: RuleExecution CRUD ---

func TestCreateRuleExecution_MissingExecution(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateRuleExecution(context.Background(), connect.NewRequest(&apiv1.CreateRuleExecutionRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateRuleExecution_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(testRule("r-1"), nil)
	s.On("CreateRuleExecution", mock.Anything, mock.Anything).Return(testExecution("e-1", "r-1"), nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.CreateRuleExecution(ctx, connect.NewRequest(&apiv1.CreateRuleExecutionRequest{
		RuleExecution: &apiv1.RuleExecution{RuleId: "r-1"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "e-1", resp.Msg.Id)
}

func TestGetRuleExecution_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.GetRuleExecution(context.Background(), connect.NewRequest(&apiv1.GetRuleExecutionRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestListRuleExecutions_WithFilters(t *testing.T) {
	s := &mockStore{}
	ruleID := "r-1"
	s.On("ListRuleExecutions", mock.Anything, ListRuleExecutionsOpts{UserID: "user-1", RuleID: "r-1"}).
		Return([]*entity.RuleExecution{testExecution("e-1", "r-1"), testExecution("e-2", "r-1")}, "next", nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	resp, err := h.ListRuleExecutions(ctx, connect.NewRequest(&apiv1.ListRuleExecutionsRequest{
		RuleId: &ruleID,
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.RuleExecutions, 2)
	assert.Equal(t, "next", resp.Msg.NextPageToken)
}

// --- Tests: Execution methods validate inputs ---

func TestExecutionMethods_ValidateInputs(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := context.Background()

	_, err := h.ExecuteRule(ctx, connect.NewRequest(&apiv1.ExecuteRuleRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = h.ExecuteRuleAsync(ctx, connect.NewRequest(&apiv1.ExecuteRuleAsyncRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = h.CancelRuleExecution(ctx, connect.NewRequest(&apiv1.CancelRuleExecutionRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = h.SimulateRule(ctx, connect.NewRequest(&apiv1.SimulateRuleRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestCreateRule_RejectsUnhonourableSchedule: cron expressions are validated
// where they are written, not at the next scheduler reload where an invalid one
// is merely skipped and the rule silently never runs. Sub-minute intervals are
// refused outright: rule executions will drive syncs and price fetches, each
// spending a provider's metered allowance.
func TestCreateRule_RejectsUnhonourableSchedule(t *testing.T) {
	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})

	tests := []struct {
		name string
		spec string
	}{
		{"sub-minute descriptor", "@every 1s"},
		{"malformed spec", "not a cron"},
		{"too many fields", "* * * * * * *"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockStore{}
			h := newHandler(s)

			_, err := h.CreateRule(ctx, connect.NewRequest(&apiv1.CreateRuleRequest{
				Rule: &apiv1.Rule{
					Name: "r", RuleType: "dca", PortfolioId: "p-1",
					Schedule: &apiv1.RuleSchedule{CronExpression: tc.spec},
				},
			}))
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			s.AssertNotCalled(t, "CreateRule", mock.Anything, mock.Anything)
		})
	}
}

// TestCreateRule_AcceptsMinuteSchedule: a minute is the floor, not a ban on
// schedules; an empty expression stays valid because one-time rules carry none.
func TestCreateRule_AcceptsMinuteSchedule(t *testing.T) {
	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})

	for _, spec := range []string{"* * * * *", "@every 1m", "0 3 * * *", ""} {
		s := &mockStore{}
		s.On("CreateRule", mock.Anything, mock.Anything).Return(testRule("r-1"), nil)
		h := newHandler(s)

		_, err := h.CreateRule(ctx, connect.NewRequest(&apiv1.CreateRuleRequest{
			Rule: &apiv1.Rule{
				Name: "r", RuleType: "dca", PortfolioId: "p-1",
				Schedule: &apiv1.RuleSchedule{CronExpression: spec},
			},
		}))
		require.NoError(t, err, "spec %q", spec)
	}
}

// TestValidateRule_ReportsScheduleProblem: the dry-run path reports the same
// problem as a write instead of accepting what CreateRule would reject.
func TestValidateRule_ReportsScheduleProblem(t *testing.T) {
	h := newHandler(&mockStore{})

	resp, err := h.ValidateRule(context.Background(), connect.NewRequest(&apiv1.ValidateRuleRequest{
		Rule: &apiv1.Rule{
			Name: "r", RuleType: "dca", PortfolioId: "p-1",
			Schedule: &apiv1.RuleSchedule{CronExpression: "@every 1s"},
		},
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Valid)
	require.Len(t, resp.Msg.ValidationErrors, 1)
	assert.Contains(t, resp.Msg.ValidationErrors[0], "once a minute")
}
