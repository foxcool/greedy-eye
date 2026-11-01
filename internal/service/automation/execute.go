package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	// validateConfiguration checked this when the rule was written; the
	// account may have been moved to another portfolio since.
	if acct.Msg.GetPortfolioId() != rule.PortfolioID {
		return prepared{}, fmt.Errorf("account %s is no longer in portfolio %s", inputs.AccountID, rule.PortfolioID)
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
		Value: raw.Shift(-entity.DecI32(p.GetDecimals())),
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

// newExecution starts an execution record of rule.
func newExecution(rule *entity.Rule, status entity.ExecutionStatus) *entity.RuleExecution {
	return &entity.RuleExecution{
		RuleID:      rule.ID,
		PortfolioID: rule.PortfolioID,
		UserID:      rule.UserID,
		Status:      status,
		StartedAt:   time.Now(),
	}
}

// dryRunExecution is the execution a run would start with, never persisted:
// the plan in execution_summary, or the reason there is none.
func dryRunExecution(rule *entity.Rule, p prepared, prepErr error) (*entity.RuleExecution, error) {
	exec := newExecution(rule, entity.ExecutionStatusCompleted)
	exec.ID = dryRunExecutionID
	exec.CompletedAt = &exec.StartedAt
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
	exec := newExecution(rule, entity.ExecutionStatusFailed)
	exec.ErrorMessage = reason.Error()
	exec.CompletedAt = &exec.StartedAt
	created, err := h.store.CreateRuleExecution(ctx, exec)
	if err != nil {
		return nil, toConnectError(err)
	}
	return created, nil
}

// Once a trade is submitted the caller going away must not cut the run
// short: a venue may already have moved holdings, and the journal and the
// record have to follow. Submitting and recording get separate budgets, so a
// submit that used up its own (waiting on the account lock, say) still leaves
// time to write down what happened.
const (
	submitTimeout = 2 * time.Minute
	recordTimeout = 30 * time.Second
)

// run creates the execution, submits each trade and records the result.
func (h *Handler) run(ctx context.Context, rule *entity.Rule, p prepared) (*entity.RuleExecution, error) {
	exec, err := h.store.CreateRuleExecution(ctx, newExecution(rule, entity.ExecutionStatusInProgress))
	if err != nil {
		return nil, toConnectError(err)
	}

	detached := context.WithoutCancel(ctx)
	submitCtx, cancelSubmit := context.WithTimeout(detached, h.submitTimeout)
	results, err := h.submit(submitCtx, p.account, exec.ID, execution.Planned(p.plan))
	cancelSubmit()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	recordCtx, cancelRecord := context.WithTimeout(detached, recordTimeout)
	defer cancelRecord()
	txIDs, journalErr := h.journal(recordCtx, p.account.ID, exec.ID, results)
	status, reason := execution.ExecutionStatus(results)
	if journalErr != nil {
		status, reason = entity.ExecutionStatusFailed, journalMissing(journalErr, reason)
	}
	summary, err := execution.Summary(results)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	now := time.Now()
	exec.Status = status
	exec.ErrorMessage = reason
	exec.CompletedAt = &now
	exec.AffectedHoldingIDs = affectedHoldings(results)
	exec.CreatedTransactionIDs = txIDs
	exec.TransactionsCreated = int32(len(txIDs)) // #nosec G115 -- one per trade of a plan
	exec.ExecutionSummary = summary
	updated, err := h.store.UpdateRuleExecution(recordCtx, exec, []string{
		"status", "error_message", "completed_at", "affected_holding_ids",
		"created_transaction_ids", "transactions_created", "execution_summary",
	})
	if err != nil {
		// The trades already happened and the row still says in_progress: the
		// log is the only place left that names what they changed.
		h.log.Error("automation: execution result not recorded",
			slog.String("execution_id", exec.ID),
			slog.String("status", apiv1.ExecutionStatus(status).String()),
			slog.String("reason", reason),
			slog.Any("affected_holding_ids", exec.AffectedHoldingIDs),
			slog.Any("created_transaction_ids", exec.CreatedTransactionIDs),
			slog.Any("error", err))
		return nil, toConnectError(err)
	}
	return updated, nil
}

// journalMissing is the reason of a run whose fills landed but whose journal
// did not. Holdings carry the fill, so the model's value is right and a
// holding can be fixed by hand; a transaction cannot be deleted through the
// API, so the journal is not retried blindly.
func journalMissing(journalErr error, tradeReason string) string {
	parts := []string{"holdings applied, journal missing: " + journalErr.Error()}
	if tradeReason != "" {
		parts = append(parts, tradeReason)
	}
	return strings.Join(parts, "; ")
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

// journal records every filled trade as a TRADE transaction on the account,
// whatever venue filled it, under import_id = the execution id.
func (h *Handler) journal(ctx context.Context, accountID, executionID string, results []execution.TradeResult) ([]string, error) {
	var items []*apiv1.ImportTransactionItem
	for i, r := range results {
		if r.State == execution.StateFilled {
			items = append(items, execution.JournalItem(execution.Key{ExecutionID: executionID, Index: i}, r))
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	resp, err := h.pClient.ImportTransactions(ctx, connect.NewRequest(&apiv1.ImportTransactionsRequest{
		AccountId:    accountID,
		Transactions: items,
		ImportId:     &executionID,
	}))
	if err != nil {
		return nil, err
	}
	var ids, failed []string
	for _, item := range resp.Msg.GetItems() {
		switch {
		case item.Error != nil:
			failed = append(failed, fmt.Sprintf("item %d: %s", item.GetIndex(), item.GetError()))
		case item.TransactionId != nil:
			ids = append(ids, item.GetTransactionId())
		}
	}
	if len(failed) > 0 {
		return ids, errors.New(strings.Join(failed, "; "))
	}
	return ids, nil
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
