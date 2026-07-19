package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

type mockRuleStore struct {
	mock.Mock
}

func (m *mockRuleStore) ListRules(ctx context.Context, opts automation.ListRulesOpts) ([]*entity.Rule, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Rule), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

type fakeExecutor struct {
	mu    sync.Mutex
	calls []*connect.Request[apiv1.ExecuteRuleRequest]
	ctxs  []context.Context
	resp  *connect.Response[apiv1.ExecuteRuleResponse]
	err   error
}

func (f *fakeExecutor) ExecuteRule(ctx context.Context, req *connect.Request[apiv1.ExecuteRuleRequest]) (*connect.Response[apiv1.ExecuteRuleResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	f.ctxs = append(f.ctxs, ctx)
	return f.resp, f.err
}

type fakeFetcher struct {
	mu    sync.Mutex
	calls []*connect.Request[apiv1.FetchExternalPricesRequest]
	resp  *connect.Response[apiv1.FetchExternalPricesResponse]
	err   error
}

func (f *fakeFetcher) FetchExternalPrices(ctx context.Context, req *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.resp, f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestScheduler(t *testing.T, cfg Config, rules RuleStore, exec RuleExecutor, prices PriceFetcher) *Scheduler {
	t.Helper()
	s, err := New(cfg, rules, exec, prices, testLogger())
	require.NoError(t, err)
	return s
}

func cronRule(id, userID, expr, tz string) *entity.Rule {
	return &entity.Rule{
		ID:     id,
		UserID: userID,
		Status: entity.RuleStatusActive,
		Schedule: &entity.RuleSchedule{
			CronExpression: expr,
			Timezone:       tz,
		},
	}
}

func expectRulesPage(store *mockRuleStore, token string, rules []*entity.Rule, nextToken string) {
	store.On("ListRules", mock.Anything, automation.ListRulesOpts{
		Status:    entity.RuleStatusActive,
		PageSize:  rulePageSize,
		PageToken: token,
	}).Return(rules, nextToken, nil).Once()
}

// --- New ---

func TestNew_InvalidPriceFetchCron(t *testing.T) {
	_, err := New(Config{PriceFetchCron: "not a cron"}, &mockRuleStore{}, &fakeExecutor{}, &fakeFetcher{}, testLogger())
	assert.Error(t, err)
}

func TestNew_EmptyPriceFetchCron(t *testing.T) {
	s := newTestScheduler(t, Config{}, &mockRuleStore{}, &fakeExecutor{}, &fakeFetcher{})
	assert.NotNil(t, s)
}

// --- reloadRules ---

func TestReloadRules_SchedulesAndSkips(t *testing.T) {
	store := &mockRuleStore{}
	oneTime := cronRule("one-time", "u1", "0 0 * * *", "")
	oneTime.Schedule.OneTime = true
	expectRulesPage(store, "", []*entity.Rule{
		cronRule("valid", "u1", "*/5 * * * *", ""),
		cronRule("with-tz", "u1", "0 12 * * *", "Europe/Moscow"),
		oneTime,
		{ID: "no-schedule", UserID: "u1", Status: entity.RuleStatusActive},
		cronRule("empty-cron", "u1", "", ""),
		cronRule("invalid-cron", "u1", "not a cron", ""),
	}, "")

	s := newTestScheduler(t, Config{}, store, &fakeExecutor{}, &fakeFetcher{})
	s.reloadRules(context.Background())

	assert.Len(t, s.entries, 2)
	assert.Contains(t, s.entries, "valid")
	assert.Contains(t, s.entries, "with-tz")
	assert.Equal(t, "CRON_TZ=Europe/Moscow 0 12 * * *", s.entries["with-tz"].spec)
	assert.Len(t, s.cron.Entries(), 2)
	store.AssertExpectations(t)
}

func TestReloadRules_DiffKeepsUnchangedSwapsChangedRemovesVanished(t *testing.T) {
	store := &mockRuleStore{}
	expectRulesPage(store, "", []*entity.Rule{
		cronRule("keep", "u1", "*/5 * * * *", ""),
		cronRule("change", "u1", "*/10 * * * *", ""),
		cronRule("vanish", "u1", "*/15 * * * *", ""),
	}, "")

	s := newTestScheduler(t, Config{}, store, &fakeExecutor{}, &fakeFetcher{})
	s.reloadRules(context.Background())
	require.Len(t, s.entries, 3)
	keepID := s.entries["keep"].id
	changeID := s.entries["change"].id

	expectRulesPage(store, "", []*entity.Rule{
		cronRule("keep", "u1", "*/5 * * * *", ""),
		cronRule("change", "u1", "*/20 * * * *", ""),
	}, "")
	s.reloadRules(context.Background())

	assert.Len(t, s.entries, 2)
	assert.Equal(t, keepID, s.entries["keep"].id, "unchanged rule must keep its cron entry")
	assert.NotEqual(t, changeID, s.entries["change"].id, "changed rule must be re-registered")
	assert.Equal(t, "*/20 * * * *", s.entries["change"].spec)
	assert.NotContains(t, s.entries, "vanish")
	assert.Len(t, s.cron.Entries(), 2)
	store.AssertExpectations(t)
}

func TestReloadRules_Pagination(t *testing.T) {
	store := &mockRuleStore{}
	expectRulesPage(store, "", []*entity.Rule{cronRule("r1", "u1", "*/5 * * * *", "")}, "tok")
	expectRulesPage(store, "tok", []*entity.Rule{cronRule("r2", "u2", "*/5 * * * *", "")}, "")

	s := newTestScheduler(t, Config{}, store, &fakeExecutor{}, &fakeFetcher{})
	s.reloadRules(context.Background())

	assert.Len(t, s.entries, 2)
	store.AssertExpectations(t)
}

func TestReloadRules_StoreErrorKeepsExistingEntries(t *testing.T) {
	store := &mockRuleStore{}
	expectRulesPage(store, "", []*entity.Rule{cronRule("r1", "u1", "*/5 * * * *", "")}, "")

	s := newTestScheduler(t, Config{}, store, &fakeExecutor{}, &fakeFetcher{})
	s.reloadRules(context.Background())
	require.Len(t, s.entries, 1)

	store.On("ListRules", mock.Anything, mock.Anything).Return(nil, "", errors.New("db down")).Once()
	s.reloadRules(context.Background())

	assert.Len(t, s.entries, 1, "failed reload must not drop existing schedules")
	store.AssertExpectations(t)
}

// --- runRule ---

func TestRunRule_PassesOwnerContextAndRuleID(t *testing.T) {
	exec := &fakeExecutor{
		resp: connect.NewResponse(&apiv1.ExecuteRuleResponse{
			Execution: &apiv1.RuleExecution{Id: "exec-1"},
		}),
	}
	s := newTestScheduler(t, Config{}, &mockRuleStore{}, exec, &fakeFetcher{})

	s.runRule("rule-1", "user-1")

	require.Len(t, exec.calls, 1)
	assert.Equal(t, "rule-1", exec.calls[0].Msg.GetRuleId())
	assert.False(t, exec.calls[0].Msg.GetDryRun())
	user, ok := middleware.UserFromContext(exec.ctxs[0])
	require.True(t, ok, "executor context must carry the rule owner")
	assert.Equal(t, "user-1", user.ID)
}

func TestRunRule_ErrorCodesHandled(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeFailedPrecondition, connect.CodeNotFound, connect.CodeInternal} {
		t.Run(code.String(), func(t *testing.T) {
			exec := &fakeExecutor{err: connect.NewError(code, fmt.Errorf("boom"))}
			s := newTestScheduler(t, Config{}, &mockRuleStore{}, exec, &fakeFetcher{})
			assert.NotPanics(t, func() { s.runRule("rule-1", "user-1") })
		})
	}
}

// --- fetchPrices ---

func TestFetchPrices_EmptyRequest(t *testing.T) {
	fetcher := &fakeFetcher{
		resp: connect.NewResponse(&apiv1.FetchExternalPricesResponse{
			PricesFetched: 10,
			PricesStored:  9,
			Errors:        []string{"provider x: partial"},
		}),
	}
	s := newTestScheduler(t, Config{PriceFetchCron: "*/15 * * * *"}, &mockRuleStore{}, &fakeExecutor{}, fetcher)

	s.fetchPrices()

	require.Len(t, fetcher.calls, 1)
	assert.Empty(t, fetcher.calls[0].Msg.GetAssetIds())
	assert.Empty(t, fetcher.calls[0].Msg.GetSourceIds())
}

func TestFetchPrices_ErrorHandled(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("network down")}
	s := newTestScheduler(t, Config{}, &mockRuleStore{}, &fakeExecutor{}, fetcher)
	assert.NotPanics(t, func() { s.fetchPrices() })
}

// --- lifecycle ---

func TestStartStop(t *testing.T) {
	store := &mockRuleStore{}
	expectRulesPage(store, "", nil, "")

	s := newTestScheduler(t, Config{PriceFetchCron: "*/15 * * * *"}, store, &fakeExecutor{}, &fakeFetcher{})
	require.NoError(t, s.Start())
	// price fetch job + rule reload job
	assert.Len(t, s.cron.Entries(), 2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Stop(ctx)
	store.AssertExpectations(t)
}
