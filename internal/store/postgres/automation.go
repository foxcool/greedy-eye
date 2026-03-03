package postgres

import (
	"context"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AutomationStore implements automation.Store using PostgreSQL.
type AutomationStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ automation.Store = (*AutomationStore)(nil)

func NewAutomationStore(pool *pgxpool.Pool) *AutomationStore {
	return &AutomationStore{pool: pool}
}

// --- Rule methods ---

func (s *AutomationStore) CreateRule(ctx context.Context, r *entity.Rule) (*entity.Rule, error) {
	// TODO: implement when rules schema is added
	return nil, fmt.Errorf("%w: automation store not yet implemented", store.ErrNotFound)
}

func (s *AutomationStore) GetRule(ctx context.Context, id string) (*entity.Rule, error) {
	// TODO: implement when rules schema is added
	return nil, fmt.Errorf("%w: rule %s not found", store.ErrNotFound, id)
}

func (s *AutomationStore) UpdateRule(ctx context.Context, r *entity.Rule, fields []string) (*entity.Rule, error) {
	// TODO: implement when rules schema is added
	return nil, fmt.Errorf("%w: rule %s not found", store.ErrNotFound, r.ID)
}

func (s *AutomationStore) DeleteRule(ctx context.Context, id string) error {
	// TODO: implement when rules schema is added
	return fmt.Errorf("%w: rule %s not found", store.ErrNotFound, id)
}

func (s *AutomationStore) ListRules(ctx context.Context, opts automation.ListRulesOpts) ([]*entity.Rule, string, error) {
	// TODO: implement when rules schema is added
	return nil, "", nil
}

// --- RuleExecution methods ---

func (s *AutomationStore) CreateRuleExecution(ctx context.Context, e *entity.RuleExecution) (*entity.RuleExecution, error) {
	// TODO: implement when rule_executions schema is added
	return nil, fmt.Errorf("%w: automation store not yet implemented", store.ErrNotFound)
}

func (s *AutomationStore) GetRuleExecution(ctx context.Context, id string) (*entity.RuleExecution, error) {
	// TODO: implement when rule_executions schema is added
	return nil, fmt.Errorf("%w: rule execution %s not found", store.ErrNotFound, id)
}

func (s *AutomationStore) UpdateRuleExecution(ctx context.Context, e *entity.RuleExecution, fields []string) (*entity.RuleExecution, error) {
	// TODO: implement when rule_executions schema is added
	return nil, fmt.Errorf("%w: rule execution %s not found", store.ErrNotFound, e.ID)
}

func (s *AutomationStore) ListRuleExecutions(ctx context.Context, opts automation.ListRuleExecutionsOpts) ([]*entity.RuleExecution, string, error) {
	// TODO: implement when rule_executions schema is added
	return nil, "", nil
}
