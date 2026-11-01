package automation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/execution"
	"github.com/shopspring/decimal"
)

// The imperative shell of rule execution: it loads what an engine needs,
// hands the plan to venues and records the run. Every decision is made by the
// pure functions of package execution.

// dryRunExecutionID marks an execution that was never persisted.
const dryRunExecutionID = "dry-run"

// prepared is a rule ready to run: the account it trades on, the snapshot its
// plan was made from, and the plan.
type prepared struct {
	account execution.Account
	snap    execution.Snapshot
	plan    execution.TradePlan
}

// ruleEngine returns the engine of the rule's type, or CodeUnimplemented.
func (h *Handler) ruleEngine(rule *entity.Rule) (execution.Engine, error) {
	eng, ok := h.engines.Lookup(rule.RuleType)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("no engine for rule type %q", rule.RuleType))
	}
	return eng, nil
}

// validateConfiguration refuses a configuration the rule's engine could not
// run: unparseable, or naming an account the caller cannot read or that sits
// outside the rule's portfolio. A rule type without an engine is stored as
// is; it cannot run until one exists.
func (h *Handler) validateConfiguration(ctx context.Context, rule *entity.Rule) error {
	eng, ok := h.engines.Lookup(rule.RuleType)
	if !ok {
		return nil
	}
	inputs, err := eng.Inputs(rule)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("configuration: %w", err))
	}
	if h.pClient == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("account lookup not configured"))
	}
	acct, err := h.pClient.GetAccount(ctx, connect.NewRequest(&apiv1.GetAccountRequest{Id: inputs.AccountID}))
	switch connect.CodeOf(err) {
	case connect.CodeNotFound, connect.CodePermissionDenied, connect.CodeInvalidArgument:
		// Not found and not yours read the same, so the answer does not tell
		// one from the other.
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("configuration: account %s not found", inputs.AccountID))
	}
	if err != nil {
		return err
	}
	if acct.Msg.GetPortfolioId() != rule.PortfolioID {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("configuration: account %s is not in portfolio %s", inputs.AccountID, rule.PortfolioID))
	}
	return nil
}

// prepare builds the snapshot and plans. Its error is the reason a run
// failed, recorded on the execution rather than returned to the caller.
func (h *Handler) prepare(ctx context.Context, eng execution.Engine, rule *entity.Rule) (prepared, error) {
	if h.pClient == nil || h.mdClient == nil {
		return prepared{}, errors.New("execution clients are not configured")
	}
	inputs, err := eng.Inputs(rule)
	if err != nil {
		return prepared{}, fmt.Errorf("invalid configuration: %w", err)
	}
	acct, err := h.pClient.GetAccount(ctx, connect.NewRequest(&apiv1.GetAccountRequest{Id: inputs.AccountID}))
	if err != nil {
		return prepared{}, fmt.Errorf("account %s: %w", inputs.AccountID, err)
	}
	snap, err := h.snapshot(ctx, inputs)
	if err != nil {
		return prepared{}, err
	}
	plan, err := eng.Plan(rule, snap)
	if err != nil {
		return prepared{}, fmt.Errorf("plan: %w", err)
	}
	return prepared{
		account: execution.Account{ID: acct.Msg.GetId(), Type: acct.Msg.GetType()},
		snap:    snap,
		plan:    plan,
	}, nil
}

// snapshot reads the account's holdings and the prices the engine asked for.
// A pair with no price is left out: saying so is Plan's decision.
func (h *Handler) snapshot(ctx context.Context, inputs execution.Inputs) (execution.Snapshot, error) {
	holdings, err := execution.AccountHoldings(ctx, h.pClient, inputs.AccountID)
	if err != nil {
		return execution.Snapshot{}, err
	}
	prices := make(map[execution.Pair]execution.Price, len(inputs.Pairs))
	for _, pair := range inputs.Pairs {
		resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
			AssetId:     pair.Asset,
			BaseAssetId: pair.Quote,
		}))
		if connect.CodeOf(err) == connect.CodeNotFound {
			continue
		}
		if err != nil {
			return execution.Snapshot{}, fmt.Errorf("price of %s in %s: %w", pair.Asset, pair.Quote, err)
		}
		price, err := priceFromProto(resp.Msg)
		if err != nil {
			return execution.Snapshot{}, err
		}
		prices[pair] = price
	}
	return execution.Snapshot{Now: time.Now(), Holdings: holdings, Prices: prices}, nil
}

// priceFromProto unscales Price.last; Price.timestamp is its age.
func priceFromProto(p *apiv1.Price) (execution.Price, error) {
	raw, err := decimal.NewFromString(p.GetLast())
	if err != nil {
		return execution.Price{}, fmt.Errorf("price %s: invalid last %q: %w", p.GetId(), p.GetLast(), err)
	}
	return execution.Price{
		Value: raw.Shift(-int32(p.GetDecimals())), // #nosec G115 -- decimals is a small storage scale
		AsOf:  p.GetTimestamp().AsTime(),
	}, nil
}

func (h *Handler) ExecuteRule(ctx context.Context, req *connect.Request[apiv1.ExecuteRuleRequest]) (*connect.Response[apiv1.ExecuteRuleResponse], error) {
	if req.Msg.RuleId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule_id is required"))
	}

	rule, err := h.ownedRule(ctx, req.Msg.RuleId)
	if err != nil {
		return nil, err
	}
	if rule.Status != entity.RuleStatusActive {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("rule is not active"))
	}
	eng, err := h.ruleEngine(rule)
	if err != nil {
		return nil, err
	}

	p, prepErr := h.prepare(ctx, eng, rule)
	var exec *entity.RuleExecution
	switch {
	case req.Msg.DryRun:
		exec, err = dryRunExecution(rule, p, prepErr)
	case prepErr != nil:
		exec, err = h.recordFailure(ctx, rule, prepErr)
	default:
		exec, err = h.run(ctx, rule, p)
	}
	if err != nil {
		return nil, err
	}

	proto, err := ruleExecutionToProto(exec)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&apiv1.ExecuteRuleResponse{Execution: proto}), nil
}

// dryRunExecution is the execution a run would start with, never persisted:
// the plan in execution_summary, or the reason there is none.
func dryRunExecution(rule *entity.Rule, p prepared, prepErr error) (*entity.RuleExecution, error) {
	now := time.Now()
	exec := &entity.RuleExecution{
		ID:          dryRunExecutionID,
		RuleID:      rule.ID,
		PortfolioID: rule.PortfolioID,
		UserID:      rule.UserID,
		Status:      entity.ExecutionStatusCompleted,
		StartedAt:   now,
		CompletedAt: &now,
	}
	if prepErr != nil {
		exec.Status = entity.ExecutionStatusFailed
		exec.ErrorMessage = prepErr.Error()
		return exec, nil
	}
	summary, err := execution.Summary(execution.Planned(p.plan))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	exec.ExecutionSummary = summary
	return exec, nil
}

// recordFailure persists a run that could not plan, so the reason is on
// record instead of in a log line nobody reads.
func (h *Handler) recordFailure(ctx context.Context, rule *entity.Rule, reason error) (*entity.RuleExecution, error) {
	now := time.Now()
	created, err := h.store.CreateRuleExecution(ctx, &entity.RuleExecution{
		RuleID:       rule.ID,
		PortfolioID:  rule.PortfolioID,
		UserID:       rule.UserID,
		Status:       entity.ExecutionStatusFailed,
		ErrorMessage: reason.Error(),
		StartedAt:    now,
		CompletedAt:  &now,
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return created, nil
}

// run creates the execution, submits each trade and records the result.
func (h *Handler) run(ctx context.Context, rule *entity.Rule, p prepared) (*entity.RuleExecution, error) {
	exec, err := h.store.CreateRuleExecution(ctx, &entity.RuleExecution{
		RuleID:      rule.ID,
		PortfolioID: rule.PortfolioID,
		UserID:      rule.UserID,
		Status:      entity.ExecutionStatusInProgress,
		StartedAt:   time.Now(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	results, err := h.submit(ctx, p.account, exec.ID, execution.Planned(p.plan))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// A venue may already have moved holdings: the record is finished even
	// when the caller has gone.
	wctx := context.WithoutCancel(ctx)

	status, reason := execution.ExecutionStatus(results)
	summary, err := execution.Summary(results)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	now := time.Now()
	exec.Status = status
	exec.ErrorMessage = reason
	exec.CompletedAt = &now
	exec.AffectedHoldingIDs = affectedHoldings(results)
	exec.ExecutionSummary = summary
	updated, err := h.store.UpdateRuleExecution(wctx, exec, []string{
		"status", "error_message", "completed_at", "affected_holding_ids", "execution_summary",
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return updated, nil
}

// submit places the trades in order on the account's venue. After a rejected
// trade the rest are not placed: a plan is not meant to run in part.
func (h *Handler) submit(ctx context.Context, account execution.Account, executionID string, results []execution.TradeResult) ([]execution.TradeResult, error) {
	venue, ok := h.venues.Lookup(account.Type)
	halted := ""
	for i := range results {
		var err error
		switch {
		case !ok:
			results[i], err = execution.Reject(results[i], fmt.Sprintf("no venue for account type %s", account.Type))
		case halted != "":
			results[i], err = execution.Reject(results[i], halted)
		default:
			outcome, submitErr := venue.Submit(ctx, account, results[i].Trade, execution.Key{ExecutionID: executionID, Index: i})
			results[i], err = execution.Settle(results[i], outcome, submitErr)
		}
		if err != nil {
			return nil, err
		}
		if results[i].State == execution.StateRejected && halted == "" {
			halted = fmt.Sprintf("not placed: trade %d was rejected", i)
		}
	}
	return results, nil
}

// affectedHoldings lists the holdings the venues changed, each once.
func affectedHoldings(results []execution.TradeResult) []string {
	seen := map[string]bool{}
	var ids []string
	for _, r := range results {
		for _, id := range r.HoldingIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (h *Handler) SimulateRule(ctx context.Context, req *connect.Request[apiv1.SimulateRuleRequest]) (*connect.Response[apiv1.SimulateRuleResponse], error) {
	if req.Msg.RuleId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule_id is required"))
	}

	rule, err := h.ownedRule(ctx, req.Msg.RuleId)
	if err != nil {
		return nil, err
	}
	eng, err := h.ruleEngine(rule)
	if err != nil {
		return nil, err
	}

	p, err := h.prepare(ctx, eng, rule)
	if err != nil {
		return connect.NewResponse(&apiv1.SimulateRuleResponse{ErrorMessage: err.Error()}), nil
	}
	return connect.NewResponse(&apiv1.SimulateRuleResponse{
		Success: true,
		Result:  eng.Simulate(rule, p.snap, p.plan),
	}), nil
}
