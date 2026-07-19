package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/automation"
)

// reloadRules re-reads all active rules and diffs them against the currently
// scheduled entries. Unchanged entries are left untouched — re-adding an
// "@every" entry would reset its interval, so a full rebuild every minute
// would prevent long intervals from ever firing.
func (s *Scheduler) reloadRules(ctx context.Context) {
	desired, err := s.desiredSchedules(ctx)
	if err != nil {
		s.log.Error("scheduler: list active rules", slog.Any("error", err))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var added, removed int
	for ruleID, entry := range s.entries {
		if _, ok := desired[ruleID]; ok && desired[ruleID].spec == entry.spec {
			continue
		}
		s.cron.Remove(entry.id)
		delete(s.entries, ruleID)
		removed++
	}
	for ruleID, want := range desired {
		if _, ok := s.entries[ruleID]; ok {
			continue
		}
		ruleID, userID := ruleID, want.userID
		entryID, err := s.cron.AddFunc(want.spec, func() { s.runRule(ruleID, userID) })
		if err != nil {
			s.log.Warn("scheduler: invalid rule schedule, skipping",
				slog.String("rule_id", ruleID), slog.String("spec", want.spec), slog.Any("error", err))
			continue
		}
		s.entries[ruleID] = ruleEntry{id: entryID, spec: want.spec}
		added++
	}
	if added > 0 || removed > 0 {
		s.log.Info("scheduler: rule schedules reloaded",
			slog.Int("added", added), slog.Int("removed", removed), slog.Int("total", len(s.entries)))
	}
}

// desiredSchedule is what a rule's cron registration should look like.
type desiredSchedule struct {
	spec   string
	userID string
}

// desiredSchedules pages through all active rules and returns the ones that
// should be on the cron: non-one-time rules with a cron expression.
func (s *Scheduler) desiredSchedules(ctx context.Context) (map[string]desiredSchedule, error) {
	desired := make(map[string]desiredSchedule)
	var pageToken string
	for {
		rules, next, err := s.rules.ListRules(ctx, automation.ListRulesOpts{
			Status:    entity.RuleStatusActive,
			PageSize:  rulePageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rules {
			if r.Schedule == nil || r.Schedule.CronExpression == "" {
				continue
			}
			if r.Schedule.OneTime {
				// One-shot scheduling (OneTime/ExecuteAfter) is not supported yet.
				s.log.Debug("scheduler: skipping one-time rule", slog.String("rule_id", r.ID))
				continue
			}
			spec := r.Schedule.CronExpression
			if r.Schedule.Timezone != "" {
				spec = "CRON_TZ=" + r.Schedule.Timezone + " " + spec
			}
			desired[r.ID] = desiredSchedule{spec: spec, userID: r.UserID}
		}
		if next == "" {
			break
		}
		pageToken = next
	}
	return desired, nil
}

// runRule fires ExecuteRule on behalf of the rule's owner. The handler
// refetches the rule, enforces ownership and records the RuleExecution —
// exactly as the RPC path does.
func (s *Scheduler) runRule(ruleID, userID string) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	ctx = middleware.ContextWithUser(ctx, &entity.User{ID: userID})

	start := time.Now()
	resp, err := s.exec.ExecuteRule(ctx, connect.NewRequest(&apiv1.ExecuteRuleRequest{RuleId: ruleID}))
	if err != nil {
		var connectErr *connect.Error
		// Paused or deleted since the last reload: expected, the next reload
		// drops the entry.
		if errors.As(err, &connectErr) &&
			(connectErr.Code() == connect.CodeFailedPrecondition || connectErr.Code() == connect.CodeNotFound) {
			s.log.Info("scheduler: rule no longer executable",
				slog.String("rule_id", ruleID), slog.String("code", connectErr.Code().String()))
			return
		}
		s.log.Error("scheduler: rule execution failed",
			slog.String("rule_id", ruleID), slog.Any("error", err))
		return
	}
	s.log.Info("scheduler: rule executed",
		slog.String("rule_id", ruleID),
		slog.String("execution_id", resp.Msg.GetExecution().GetId()),
		slog.Duration("duration", time.Since(start)))
}
