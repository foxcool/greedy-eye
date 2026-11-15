package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/execution"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// Executions whose trades wait on someone else — a signature, then a block —
// are stored as pending, with each trade's state in execution_summary. This
// file moves them on: a rule's next run replaces its unsigned proposal, and a
// scheduled pass asks the venue where each waiting trade is.

// expiredBySuccessor is the reason a proposal expires when its rule runs again.
const expiredBySuccessor = "not signed before the rule's next run"

// waitingPageSize is how many waiting executions one store read returns.
const waitingPageSize = 100

// supersede keeps one waiting proposal per rule. Before a rule runs again its
// unsigned proposals expire: the next run proposes afresh at a current quote,
// and missed purchases do not pile up. A trade already submitted is on its way
// to a block and cannot be withdrawn, so while one exists the new run does not
// start; the reason comes back as blocked.
func (h *Handler) supersede(ctx context.Context, rule *entity.Rule) (blocked string, err error) {
	waiting, err := h.waitingExecutions(ctx, ListRuleExecutionsOpts{RuleID: rule.ID})
	if err != nil {
		return "", err
	}
	for _, exec := range waiting {
		results, err := execution.ParseSummary(exec.ExecutionSummary)
		if err != nil {
			return "", connect.NewError(connect.CodeInternal, fmt.Errorf("execution %s: %w", exec.ID, err))
		}
		for _, r := range results {
			if r.State == execution.StateSubmitted {
				return fmt.Sprintf("execution %s has a transaction waiting for its block", exec.ID), nil
			}
		}
		for i := range results {
			if results[i].State == execution.StatePendingSignature {
				if results[i], err = execution.Expire(results[i], expiredBySuccessor); err != nil {
					return "", connect.NewError(connect.CodeInternal, err)
				}
			}
		}
		if err := h.recordResults(ctx, exec, results); err != nil {
			return "", err
		}
	}
	return "", nil
}

// waitingExecutions lists every pending execution matching opts, all pages.
func (h *Handler) waitingExecutions(ctx context.Context, opts ListRuleExecutionsOpts) ([]*entity.RuleExecution, error) {
	opts.Status = entity.ExecutionStatusPending
	opts.PageSize = waitingPageSize
	var all []*entity.RuleExecution
	for {
		page, next, err := h.store.ListRuleExecutions(ctx, opts)
		if err != nil {
			return nil, toConnectError(err)
		}
		all = append(all, page...)
		if next == "" || len(page) == 0 {
			return all, nil
		}
		opts.PageToken = next
	}
}

// recordResults writes trades back onto their execution with the status they
// give. A status that leaves pending also gets its completion time.
func (h *Handler) recordResults(ctx context.Context, exec *entity.RuleExecution, results []execution.TradeResult) error {
	summary, err := execution.Summary(results)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	status, reason := execution.ExecutionStatus(results)
	exec.Status = status
	exec.ErrorMessage = reason
	exec.ExecutionSummary = summary
	fields := []string{"status", "error_message", "execution_summary"}
	if status != entity.ExecutionStatusPending {
		now := time.Now()
		exec.CompletedAt = &now
		fields = append(fields, "completed_at")
	}
	if _, err := h.store.UpdateRuleExecution(ctx, exec, fields); err != nil {
		return toConnectError(err)
	}
	return nil
}

// ConfirmWaiting asks the venue of every waiting execution where its trades
// are now, and records what moved. It runs from the scheduler, for every
// user: each execution is checked on behalf of its owner, so the account read
// passes the same ownership checks as a run. One execution failing to check
// does not stop the others; the count is of executions whose status changed.
func (h *Handler) ConfirmWaiting(ctx context.Context) (moved int, err error) {
	waiting, err := h.waitingExecutions(ctx, ListRuleExecutionsOpts{})
	if err != nil {
		return 0, err
	}
	for _, exec := range waiting {
		changed, err := h.confirmOne(middleware.ContextWithUser(ctx, &entity.User{ID: exec.UserID}), exec)
		if err != nil {
			h.log.Warn("automation: waiting execution not checked",
				slog.String("execution_id", exec.ID), slog.Any("error", err))
			continue
		}
		if changed {
			moved++
		}
	}
	return moved, nil
}

func (h *Handler) confirmOne(ctx context.Context, exec *entity.RuleExecution) (bool, error) {
	results, err := execution.ParseSummary(exec.ExecutionSummary)
	if err != nil {
		return false, err
	}
	rule, err := h.store.GetRule(ctx, exec.RuleID)
	if err != nil {
		return false, fmt.Errorf("rule %s: %w", exec.RuleID, err)
	}
	eng, err := h.ruleEngine(rule)
	if err != nil {
		return false, err
	}
	inputs, err := eng.Inputs(rule)
	if err != nil {
		return false, fmt.Errorf("rule %s configuration: %w", rule.ID, err)
	}
	if h.pClient == nil {
		return false, fmt.Errorf("account lookup not configured")
	}
	acct, err := h.pClient.GetAccount(ctx, connect.NewRequest(&apiv1.GetAccountRequest{Id: inputs.AccountID}))
	if err != nil {
		return false, fmt.Errorf("account %s: %w", inputs.AccountID, err)
	}
	account := execution.Account{ID: acct.Msg.GetId(), Type: acct.Msg.GetType()}
	v, ok := h.venues.Lookup(account.Type)
	if !ok {
		return false, fmt.Errorf("no venue for account type %s", account.Type)
	}
	venue, ok := v.(execution.WaitingVenue)
	if !ok {
		return false, fmt.Errorf("venue for account type %s cannot be asked about a waiting trade", account.Type)
	}

	before, _ := json.Marshal(results)
	for i := range results {
		if !results[i].State.Waiting() {
			continue
		}
		outcome, err := venue.Check(ctx, account, execution.Key{ExecutionID: exec.ID, Index: i}, results[i])
		if err != nil {
			// The venue could not answer; the trade waits for the next pass.
			h.log.Warn("automation: waiting trade not checked",
				slog.String("execution_id", exec.ID), slog.Int("trade", i), slog.Any("error", err))
			continue
		}
		if results[i], err = execution.Apply(results[i], outcome); err != nil {
			return false, fmt.Errorf("trade %d: %w", i, err)
		}
	}
	after, _ := json.Marshal(results)
	if string(before) == string(after) {
		return false, nil
	}
	if err := h.recordResults(ctx, exec, results); err != nil {
		return false, err
	}
	return true, nil
}
