package automation

import (
	"context"
	"encoding/json"
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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testExecID  = "0190a3a0-0000-7000-8000-000000000001"
	testAccount = "0190a3a0-0000-7000-8000-00000000000a"
	testBase    = "0190a3a0-0000-7000-8000-00000000000b"
	testQuote   = "0190a3a0-0000-7000-8000-00000000000c"
)

// fakePortfolio is an in-memory portfolio service for one manual account.
type fakePortfolio struct {
	mu          sync.Mutex
	accountType apiv1.AccountType
	portfolioID string
	holdings    []*apiv1.Holding
}

func (f *fakePortfolio) GetAccount(_ context.Context, req *connect.Request[apiv1.GetAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Id != testAccount {
		return nil, connect.NewError(connect.CodeNotFound, assert.AnError)
	}
	pid := f.portfolioID
	return connect.NewResponse(&apiv1.Account{Id: testAccount, Type: f.accountType, PortfolioId: &pid}), nil
}

func (f *fakePortfolio) ListHoldings(_ context.Context, _ *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return connect.NewResponse(&apiv1.ListHoldingsResponse{Holdings: f.holdings}), nil
}

type fakeMD struct {
	price *apiv1.Price
}

func (f *fakeMD) GetLatestPrice(_ context.Context, _ *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	if f.price == nil {
		return nil, connect.NewError(connect.CodeNotFound, assert.AnError)
	}
	return connect.NewResponse(f.price), nil
}

// fakeEngine plans one trade per call and reports which rule it saw.
type fakeEngine struct {
	planned []string
}

func (e *fakeEngine) Type() string { return "fake" }

func (e *fakeEngine) Inputs(*entity.Rule) (execution.Inputs, error) {
	return execution.Inputs{AccountID: testAccount}, nil
}

func (e *fakeEngine) Plan(rule *entity.Rule, snap execution.Snapshot) (execution.TradePlan, error) {
	e.planned = append(e.planned, rule.ID)
	return execution.TradePlan{Trades: []execution.Trade{{
		Side: execution.SideBuy, AssetID: "base", QuoteAssetID: "quote",
		QuoteAmount: decimal.NewFromInt(100), Price: decimal.NewFromInt(50), PriceAsOf: snap.Now,
	}}}, nil
}

func (e *fakeEngine) Simulate(*entity.Rule, execution.Snapshot, execution.TradePlan) *apiv1.SimulationResult {
	return &apiv1.SimulationResult{EstimatedSteps: 1}
}

// fakeVenue fills every trade and records the keys it was given.
type fakeVenue struct {
	keys []execution.Key
}

func (v *fakeVenue) Submit(_ context.Context, _ execution.Account, t execution.Trade, key execution.Key) (execution.Outcome, error) {
	v.keys = append(v.keys, key)
	return execution.Outcome{
		Status:     execution.OutcomeFilled,
		Fill:       execution.Fill{BaseAmount: decimal.NewFromInt(2), QuoteAmount: t.QuoteAmount, Price: t.Price, PriceAsOf: t.PriceAsOf},
		HoldingIDs: []string{"h-base", "h-quote"},
	}, nil
}

func ownerCtx() context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
}

func fakeRule(ruleType string) *entity.Rule {
	r := testRule("r-1")
	r.RuleType = ruleType
	return r
}

// execHandler wires a handler with the fake engine and, when venue is set, a
// venue for manual accounts.
func execHandler(s Store, eng execution.Engine, venue execution.Venue, accountType apiv1.AccountType) *Handler {
	venues := execution.Venues{}
	if venue != nil {
		venues[apiv1.AccountType_ACCOUNT_TYPE_MANUAL] = venue
	}
	return newHandler(s).
		WithEngines(execution.NewEngines(eng)).
		WithVenues(venues).
		WithPortfolioClient(&fakePortfolio{accountType: accountType, portfolioID: "p-1"}).
		WithMarketDataClient(&fakeMD{price: &apiv1.Price{Last: "5000", Decimals: 2, Timestamp: timestamppb.New(time.Now())}})
}

func TestSimulateRule_DispatchesToRegisteredEngine(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	eng := &fakeEngine{}
	h := execHandler(s, eng, nil, apiv1.AccountType_ACCOUNT_TYPE_MANUAL)

	resp, err := h.SimulateRule(ownerCtx(), connect.NewRequest(&apiv1.SimulateRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Success, resp.Msg.ErrorMessage)
	assert.Equal(t, int32(1), resp.Msg.Result.GetEstimatedSteps(), "the result must come from the engine")
	assert.Equal(t, []string{"r-1"}, eng.planned)
	s.AssertNotCalled(t, "CreateRuleExecution", mock.Anything, mock.Anything)
}

// expectRun records the execution as the store would and returns what the
// final update wrote.
func expectRun(s *mockStore) *entity.RuleExecution {
	final := &entity.RuleExecution{}
	s.On("CreateRuleExecution", mock.Anything, mock.MatchedBy(func(e *entity.RuleExecution) bool {
		return e.Status == entity.ExecutionStatusInProgress
	})).Return(&entity.RuleExecution{ID: testExecID, RuleID: "r-1", Status: entity.ExecutionStatusInProgress}, nil)
	s.On("UpdateRuleExecution", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { *final = *args.Get(1).(*entity.RuleExecution) }).
		Return(final, nil)
	return final
}

func TestExecuteRule_ResolvesVenueByAccountType(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	final := expectRun(s)
	venue := &fakeVenue{}
	h := execHandler(s, &fakeEngine{}, venue, apiv1.AccountType_ACCOUNT_TYPE_MANUAL)

	resp, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.Equal(t, []execution.Key{{ExecutionID: testExecID, Index: 0}}, venue.keys)
	assert.Equal(t, apiv1.ExecutionStatus_EXECUTION_STATUS_COMPLETED, resp.Msg.Execution.Status)
	assert.Equal(t, entity.ExecutionStatusCompleted, final.Status)
	assert.Equal(t, []string{"h-base", "h-quote"}, final.AffectedHoldingIDs)
	assert.NotNil(t, final.CompletedAt)
}

func TestExecuteRule_NoVenueForAccountType_Failed(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(fakeRule("fake"), nil)
	final := expectRun(s)
	venue := &fakeVenue{}
	h := execHandler(s, &fakeEngine{}, venue, apiv1.AccountType_ACCOUNT_TYPE_WALLET)

	resp, err := h.ExecuteRule(ownerCtx(), connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.Empty(t, venue.keys, "a venue for another account type must not be used")
	assert.Equal(t, apiv1.ExecutionStatus_EXECUTION_STATUS_FAILED, resp.Msg.Execution.Status)
	assert.Contains(t, final.ErrorMessage, "no venue for account type")
	assert.Contains(t, string(final.ExecutionSummary), `"state":"rejected"`)
}

// --- DCA ---

func dcaConfig(t *testing.T, cfg map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(cfg)
	require.NoError(t, err)
	return s
}

func validDCA() map[string]any {
	return map[string]any{
		"account_id":     testAccount,
		"asset_id":       testBase,
		"quote_asset_id": testQuote,
		"quote_amount":   "100",
	}
}

func dcaHandler(s Store, pc *fakePortfolio, md *fakeMD) *Handler {
	return newHandler(s).
		WithEngines(execution.NewEngines(execution.DCA{})).
		WithPortfolioClient(pc).
		WithMarketDataClient(md)
}

func TestCreateRule_DCAConfigurationValidated(t *testing.T) {
	with := func(key string, value any) map[string]any {
		cfg := validDCA()
		if value == nil {
			delete(cfg, key)
		} else {
			cfg[key] = value
		}
		return cfg
	}
	tests := []struct {
		name        string
		cfg         map[string]any
		portfolioID string
	}{
		{"empty asset_id", with("asset_id", ""), "p-1"},
		{"zero quote_amount", with("quote_amount", "0"), "p-1"},
		{"negative quote_amount", with("quote_amount", "-1"), "p-1"},
		{"quote_amount not a number", with("quote_amount", "ten"), "p-1"},
		{"missing account_id", with("account_id", nil), "p-1"},
		{"unknown account", with("account_id", "0190a3a0-0000-7000-8000-0000000000ff"), "p-1"},
		{"account outside the rule's portfolio", validDCA(), "p-other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockStore{}
			h := dcaHandler(s, &fakePortfolio{portfolioID: tc.portfolioID}, &fakeMD{})
			_, err := h.CreateRule(ownerCtx(), connect.NewRequest(&apiv1.CreateRuleRequest{Rule: &apiv1.Rule{
				Name: "dca", RuleType: execution.DCAType, PortfolioId: "p-1", Configuration: dcaConfig(t, tc.cfg),
			}}))
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), err.Error())
			s.AssertNotCalled(t, "CreateRule", mock.Anything, mock.Anything)
		})
	}

	t.Run("valid", func(t *testing.T) {
		s := &mockStore{}
		s.On("CreateRule", mock.Anything, mock.Anything).Return(fakeRule(execution.DCAType), nil)
		h := dcaHandler(s, &fakePortfolio{portfolioID: "p-1"}, &fakeMD{})
		_, err := h.CreateRule(ownerCtx(), connect.NewRequest(&apiv1.CreateRuleRequest{Rule: &apiv1.Rule{
			Name: "dca", RuleType: execution.DCAType, PortfolioId: "p-1", Configuration: dcaConfig(t, validDCA()),
		}}))
		require.NoError(t, err)
	})
}

func TestUpdateRule_DCAConfigurationValidated(t *testing.T) {
	bad := validDCA()
	bad["quote_amount"] = "0"
	for _, tc := range []struct {
		name string
		mask []string
	}{
		{"full update", nil},
		{"configuration in mask", []string{"name", "configuration"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockStore{}
			s.On("GetRule", mock.Anything, "r-1").Return(fakeRule(execution.DCAType), nil)
			h := dcaHandler(s, &fakePortfolio{portfolioID: "p-1"}, &fakeMD{})
			req := &apiv1.UpdateRuleRequest{Rule: &apiv1.Rule{Id: "r-1", Name: "dca", Configuration: dcaConfig(t, bad)}}
			if tc.mask != nil {
				req.UpdateMask = &fieldmaskpb.FieldMask{Paths: tc.mask}
			}
			_, err := h.UpdateRule(ownerCtx(), connect.NewRequest(req))
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			s.AssertNotCalled(t, "UpdateRule", mock.Anything, mock.Anything, mock.Anything)
		})
	}

	t.Run("mask without configuration is not judged", func(t *testing.T) {
		s := &mockStore{}
		s.On("GetRule", mock.Anything, "r-1").Return(fakeRule(execution.DCAType), nil)
		s.On("UpdateRule", mock.Anything, mock.Anything, []string{"name"}).Return(fakeRule(execution.DCAType), nil)
		h := dcaHandler(s, &fakePortfolio{portfolioID: "p-1"}, &fakeMD{})
		_, err := h.UpdateRule(ownerCtx(), connect.NewRequest(&apiv1.UpdateRuleRequest{
			Rule:       &apiv1.Rule{Id: "r-1", Name: "renamed"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}))
		require.NoError(t, err)
	})
}

func storedDCARule(t *testing.T) *entity.Rule {
	t.Helper()
	raw, err := json.Marshal(validDCA())
	require.NoError(t, err)
	r := fakeRule(execution.DCAType)
	r.Configuration = raw
	return r
}

func TestSimulateRule_DCA(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(storedDCARule(t), nil)
	pc := &fakePortfolio{portfolioID: "p-1", accountType: apiv1.AccountType_ACCOUNT_TYPE_MANUAL, holdings: []*apiv1.Holding{
		{Id: "h-q", AssetId: testQuote, AccountId: testAccount, Amount: "25000", Decimals: 2}, // 250.00
	}}
	md := &fakeMD{price: &apiv1.Price{Last: "4000000", Decimals: 2, Timestamp: timestamppb.New(time.Now().Add(-time.Minute))}} // 40000.00
	h := dcaHandler(s, pc, md)

	resp, err := h.SimulateRule(ownerCtx(), connect.NewRequest(&apiv1.SimulateRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	require.True(t, resp.Msg.Success, resp.Msg.ErrorMessage)
	dca := resp.Msg.Result.GetDca()
	require.NotNil(t, dca)
	assert.Equal(t, 100.0, dca.DcaAmount)
	assert.Equal(t, testBase, dca.TargetAssetId)
	assert.Equal(t, 40000.0, dca.EstimatedPrice)
	assert.Equal(t, 0.0025, dca.EstimatedPurchaseAmount)
	assert.Equal(t, 250.0, dca.AvailableBalance)
	assert.True(t, dca.SufficientFunds)
	assert.Zero(t, dca.EstimatedFee)
}

func TestSimulateRule_DCAStalePrice(t *testing.T) {
	s := &mockStore{}
	s.On("GetRule", mock.Anything, "r-1").Return(storedDCARule(t), nil)
	md := &fakeMD{price: &apiv1.Price{Last: "4000000", Decimals: 2, Timestamp: timestamppb.New(time.Now().Add(-2 * time.Hour))}}
	h := dcaHandler(s, &fakePortfolio{portfolioID: "p-1"}, md)

	resp, err := h.SimulateRule(ownerCtx(), connect.NewRequest(&apiv1.SimulateRuleRequest{RuleId: "r-1"}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Success)
	assert.Contains(t, resp.Msg.ErrorMessage, "old")
}
