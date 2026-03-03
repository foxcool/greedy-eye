package automation

import (
	"context"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Store defines the data access contract for AutomationService.
// Interface is defined here (consumer) per Go idiom "accept interfaces, return structs".
type Store interface {
	// Rules
	CreateRule(ctx context.Context, r *entity.Rule) (*entity.Rule, error)
	GetRule(ctx context.Context, id string) (*entity.Rule, error)
	UpdateRule(ctx context.Context, r *entity.Rule, fields []string) (*entity.Rule, error)
	DeleteRule(ctx context.Context, id string) error
	ListRules(ctx context.Context, opts ListRulesOpts) ([]*entity.Rule, string, error)

	// RuleExecutions
	CreateRuleExecution(ctx context.Context, e *entity.RuleExecution) (*entity.RuleExecution, error)
	GetRuleExecution(ctx context.Context, id string) (*entity.RuleExecution, error)
	UpdateRuleExecution(ctx context.Context, e *entity.RuleExecution, fields []string) (*entity.RuleExecution, error)
	ListRuleExecutions(ctx context.Context, opts ListRuleExecutionsOpts) ([]*entity.RuleExecution, string, error)
}

// ListRulesOpts contains options for listing rules.
type ListRulesOpts struct {
	UserID      string
	PortfolioID string
	RuleType    string
	Status      entity.RuleStatus
	PageSize    int
	PageToken   string
}

// ListRuleExecutionsOpts contains options for listing rule executions.
type ListRuleExecutionsOpts struct {
	RuleID      string
	PortfolioID string
	UserID      string
	Status      entity.ExecutionStatus
	From        *time.Time
	To          *time.Time
	PageSize    int
	PageToken   string
}
