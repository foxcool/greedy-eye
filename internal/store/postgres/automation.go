package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	if r == nil {
		return nil, fmt.Errorf("%w: rule is required", store.ErrInvalidArgument)
	}
	if r.Name == "" {
		return nil, fmt.Errorf("%w: rule name is required", store.ErrInvalidArgument)
	}
	if r.UserID == "" {
		return nil, fmt.Errorf("%w: user_id is required", store.ErrInvalidArgument)
	}
	if r.PortfolioID == "" {
		return nil, fmt.Errorf("%w: portfolio_id is required", store.ErrInvalidArgument)
	}
	if r.RuleType == "" {
		return nil, fmt.Errorf("%w: rule_type is required", store.ErrInvalidArgument)
	}

	userInternalID, err := s.getUserInternalID(ctx, r.UserID)
	if err != nil {
		return nil, err
	}

	portfolioInternalID, err := s.getPortfolioInternalID(ctx, r.PortfolioID)
	if err != nil {
		return nil, err
	}

	r.ID = uuid.New().String()

	configJSON := r.Configuration
	if len(configJSON) == 0 {
		configJSON = json.RawMessage("{}")
	}

	scheduleJSON, err := marshalSchedule(r.Schedule)
	if err != nil {
		return nil, err
	}

	query := `
		INSERT INTO rules (uuid, user_id, portfolio_id, name, description, rule_type, status, configuration, schedule, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		r.ID,
		userInternalID,
		portfolioInternalID,
		r.Name,
		nullableString(r.Description),
		r.RuleType,
		ruleStatusToString(r.Status),
		configJSON,
		scheduleJSON,
	).Scan(&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create rule: %w", err)
	}

	return r, nil
}

func (s *AutomationStore) GetRule(ctx context.Context, id string) (*entity.Rule, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: rule ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid rule ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT r.uuid, u.uuid, p.uuid, r.name, r.description, r.rule_type, r.status, r.configuration, r.schedule, r.created_at, r.updated_at
		FROM rules r
		JOIN users u ON r.user_id = u.id
		JOIN portfolios p ON r.portfolio_id = p.id
		WHERE r.uuid = $1`

	var r entity.Rule
	var description *string
	var statusStr string
	var configJSON []byte
	var scheduleJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&r.ID,
		&r.UserID,
		&r.PortfolioID,
		&r.Name,
		&description,
		&r.RuleType,
		&statusStr,
		&configJSON,
		&scheduleJSON,
		&r.CreatedAt,
		&r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: rule with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get rule: %w", err)
	}

	if description != nil {
		r.Description = *description
	}
	r.Status = stringToRuleStatus(statusStr)
	r.Configuration = json.RawMessage(configJSON)

	if len(scheduleJSON) > 0 {
		sched, err := unmarshalSchedule(scheduleJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal schedule: %w", err)
		}
		r.Schedule = sched
	}

	return &r, nil
}

func (s *AutomationStore) UpdateRule(ctx context.Context, r *entity.Rule, fields []string) (*entity.Rule, error) {
	if r == nil || r.ID == "" {
		return nil, fmt.Errorf("%w: rule with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(r.ID) {
		return nil, fmt.Errorf("%w: invalid rule ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{r.ID}
	argIdx := 2

	updateFields := fields
	if len(updateFields) == 0 {
		updateFields = []string{"name", "description", "status", "configuration", "schedule"}
	}

	for _, field := range updateFields {
		switch field {
		case "name":
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, r.Name)
			argIdx++
		case "description":
			setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
			args = append(args, nullableString(r.Description))
			argIdx++
		case "status":
			setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
			args = append(args, ruleStatusToString(r.Status))
			argIdx++
		case "configuration":
			configJSON := r.Configuration
			if len(configJSON) == 0 {
				configJSON = json.RawMessage("{}")
			}
			setClauses = append(setClauses, fmt.Sprintf("configuration = $%d", argIdx))
			args = append(args, configJSON)
			argIdx++
		case "schedule":
			scheduleJSON, err := marshalSchedule(r.Schedule)
			if err != nil {
				return nil, err
			}
			setClauses = append(setClauses, fmt.Sprintf("schedule = $%d", argIdx))
			args = append(args, scheduleJSON)
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE rules
		SET %s
		WHERE uuid = $1`,
		strings.Join(setClauses, ", "))

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update rule: %w", err)
	}

	if result.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: rule with ID %s", store.ErrNotFound, r.ID)
	}

	return s.GetRule(ctx, r.ID)
}

func (s *AutomationStore) DeleteRule(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: rule ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid rule ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM rules WHERE uuid = $1", id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: cannot delete rule due to existing dependencies", store.ErrConstraint)
		}
		return fmt.Errorf("failed to delete rule: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: rule with ID %s", store.ErrNotFound, id)
	}

	return nil
}

func (s *AutomationStore) ListRules(ctx context.Context, opts automation.ListRulesOpts) ([]*entity.Rule, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.UserID != "" {
		userInternalID, err := s.getUserInternalID(ctx, opts.UserID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("r.user_id = $%d", argIdx))
		args = append(args, userInternalID)
		argIdx++
	}

	if opts.PortfolioID != "" {
		portfolioInternalID, err := s.getPortfolioInternalID(ctx, opts.PortfolioID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("r.portfolio_id = $%d", argIdx))
		args = append(args, portfolioInternalID)
		argIdx++
	}

	if opts.RuleType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("r.rule_type = $%d", argIdx))
		args = append(args, opts.RuleType)
		argIdx++
	}

	if opts.Status != entity.RuleStatusUnknown {
		whereClauses = append(whereClauses, fmt.Sprintf("r.status = $%d", argIdx))
		args = append(args, ruleStatusToString(opts.Status))
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("r.uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT r.uuid, u.uuid, p.uuid, r.name, r.description, r.rule_type, r.status, r.configuration, r.schedule, r.created_at, r.updated_at
		FROM rules r
		JOIN users u ON r.user_id = u.id
		JOIN portfolios p ON r.portfolio_id = p.id
		%s
		ORDER BY r.uuid
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list rules: %w", err)
	}
	defer rows.Close()

	rules := make([]*entity.Rule, 0, limit)
	for rows.Next() {
		var r entity.Rule
		var description *string
		var statusStr string
		var configJSON []byte
		var scheduleJSON []byte

		if err := rows.Scan(
			&r.ID,
			&r.UserID,
			&r.PortfolioID,
			&r.Name,
			&description,
			&r.RuleType,
			&statusStr,
			&configJSON,
			&scheduleJSON,
			&r.CreatedAt,
			&r.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan rule: %w", err)
		}

		if description != nil {
			r.Description = *description
		}
		r.Status = stringToRuleStatus(statusStr)
		r.Configuration = json.RawMessage(configJSON)

		if len(scheduleJSON) > 0 {
			sched, err := unmarshalSchedule(scheduleJSON)
			if err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal schedule: %w", err)
			}
			r.Schedule = sched
		}

		rules = append(rules, &r)
	}

	var nextPageToken string
	if len(rules) > limit {
		lastItem := rules[limit-1]
		rules = rules[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return rules, nextPageToken, nil
}

// --- RuleExecution methods ---

func (s *AutomationStore) CreateRuleExecution(ctx context.Context, e *entity.RuleExecution) (*entity.RuleExecution, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: rule execution is required", store.ErrInvalidArgument)
	}
	if e.RuleID == "" {
		return nil, fmt.Errorf("%w: rule_id is required", store.ErrInvalidArgument)
	}

	ruleInternalID, err := s.getRuleInternalID(ctx, e.RuleID)
	if err != nil {
		return nil, err
	}

	var portfolioInternalID *int64
	if e.PortfolioID != "" {
		id, err := s.getPortfolioInternalID(ctx, e.PortfolioID)
		if err != nil {
			return nil, err
		}
		portfolioInternalID = &id
	}

	var userInternalID *int64
	if e.UserID != "" {
		id, err := s.getUserInternalID(ctx, e.UserID)
		if err != nil {
			return nil, err
		}
		userInternalID = &id
	}

	e.ID = uuid.New().String()

	txIDs := e.CreatedTransactionIDs
	if txIDs == nil {
		txIDs = []string{}
	}
	holdingIDs := e.AffectedHoldingIDs
	if holdingIDs == nil {
		holdingIDs = []string{}
	}

	txIDsJSON, err := json.Marshal(txIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal created_transaction_ids: %w", err)
	}
	holdingIDsJSON, err := json.Marshal(holdingIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal affected_holding_ids: %w", err)
	}

	startedAt := e.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
		e.StartedAt = startedAt
	}

	query := `
		INSERT INTO rule_executions (
			uuid, rule_id, portfolio_id, user_id, status, error_message,
			created_transaction_ids, affected_holding_ids, transactions_created,
			execution_summary, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	var summaryJSON []byte
	if len(e.ExecutionSummary) > 0 {
		summaryJSON = e.ExecutionSummary
	}

	_, err = s.pool.Exec(ctx, query,
		e.ID,
		ruleInternalID,
		portfolioInternalID,
		userInternalID,
		executionStatusToString(e.Status),
		nullableString(e.ErrorMessage),
		txIDsJSON,
		holdingIDsJSON,
		e.TransactionsCreated,
		summaryJSON,
		startedAt,
		e.CompletedAt,
	)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create rule execution: %w", err)
	}

	return e, nil
}

func (s *AutomationStore) GetRuleExecution(ctx context.Context, id string) (*entity.RuleExecution, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: rule execution ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid rule execution ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT re.uuid, r.uuid, p.uuid, u.uuid, re.status, re.error_message,
		       re.created_transaction_ids, re.affected_holding_ids, re.transactions_created,
		       re.execution_summary, re.started_at, re.completed_at
		FROM rule_executions re
		JOIN rules r ON re.rule_id = r.id
		LEFT JOIN portfolios p ON re.portfolio_id = p.id
		LEFT JOIN users u ON re.user_id = u.id
		WHERE re.uuid = $1`

	var e entity.RuleExecution
	var portfolioID *string
	var userID *string
	var statusStr string
	var errorMessage *string
	var txIDsJSON []byte
	var holdingIDsJSON []byte
	var summaryJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&e.ID,
		&e.RuleID,
		&portfolioID,
		&userID,
		&statusStr,
		&errorMessage,
		&txIDsJSON,
		&holdingIDsJSON,
		&e.TransactionsCreated,
		&summaryJSON,
		&e.StartedAt,
		&e.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: rule execution with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get rule execution: %w", err)
	}

	if portfolioID != nil {
		e.PortfolioID = *portfolioID
	}
	if userID != nil {
		e.UserID = *userID
	}
	if errorMessage != nil {
		e.ErrorMessage = *errorMessage
	}
	e.Status = stringToExecutionStatus(statusStr)

	if err := json.Unmarshal(txIDsJSON, &e.CreatedTransactionIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal created_transaction_ids: %w", err)
	}
	if err := json.Unmarshal(holdingIDsJSON, &e.AffectedHoldingIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal affected_holding_ids: %w", err)
	}
	if len(summaryJSON) > 0 {
		e.ExecutionSummary = json.RawMessage(summaryJSON)
	}

	return &e, nil
}

func (s *AutomationStore) UpdateRuleExecution(ctx context.Context, e *entity.RuleExecution, fields []string) (*entity.RuleExecution, error) {
	if e == nil || e.ID == "" {
		return nil, fmt.Errorf("%w: rule execution with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(e.ID) {
		return nil, fmt.Errorf("%w: invalid rule execution ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{}
	args := []any{e.ID}
	argIdx := 2

	updateFields := fields
	if len(updateFields) == 0 {
		updateFields = []string{"status", "error_message", "completed_at", "execution_summary", "created_transaction_ids", "affected_holding_ids", "transactions_created"}
	}

	for _, field := range updateFields {
		switch field {
		case "status":
			setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
			args = append(args, executionStatusToString(e.Status))
			argIdx++
		case "error_message":
			setClauses = append(setClauses, fmt.Sprintf("error_message = $%d", argIdx))
			args = append(args, nullableString(e.ErrorMessage))
			argIdx++
		case "completed_at":
			setClauses = append(setClauses, fmt.Sprintf("completed_at = $%d", argIdx))
			args = append(args, e.CompletedAt)
			argIdx++
		case "execution_summary":
			var summaryJSON []byte
			if len(e.ExecutionSummary) > 0 {
				summaryJSON = e.ExecutionSummary
			}
			setClauses = append(setClauses, fmt.Sprintf("execution_summary = $%d", argIdx))
			args = append(args, summaryJSON)
			argIdx++
		case "created_transaction_ids":
			txIDs := e.CreatedTransactionIDs
			if txIDs == nil {
				txIDs = []string{}
			}
			txIDsJSON, err := json.Marshal(txIDs)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal created_transaction_ids: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("created_transaction_ids = $%d", argIdx))
			args = append(args, txIDsJSON)
			argIdx++
		case "affected_holding_ids":
			holdingIDs := e.AffectedHoldingIDs
			if holdingIDs == nil {
				holdingIDs = []string{}
			}
			holdingIDsJSON, err := json.Marshal(holdingIDs)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal affected_holding_ids: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("affected_holding_ids = $%d", argIdx))
			args = append(args, holdingIDsJSON)
			argIdx++
		case "transactions_created":
			setClauses = append(setClauses, fmt.Sprintf("transactions_created = $%d", argIdx))
			args = append(args, e.TransactionsCreated)
			argIdx++
		}
	}

	if len(setClauses) == 0 {
		return s.GetRuleExecution(ctx, e.ID)
	}

	query := fmt.Sprintf(`
		UPDATE rule_executions
		SET %s
		WHERE uuid = $1`,
		strings.Join(setClauses, ", "))

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update rule execution: %w", err)
	}

	if result.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: rule execution with ID %s", store.ErrNotFound, e.ID)
	}

	return s.GetRuleExecution(ctx, e.ID)
}

func (s *AutomationStore) ListRuleExecutions(ctx context.Context, opts automation.ListRuleExecutionsOpts) ([]*entity.RuleExecution, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.RuleID != "" {
		ruleInternalID, err := s.getRuleInternalID(ctx, opts.RuleID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("re.rule_id = $%d", argIdx))
		args = append(args, ruleInternalID)
		argIdx++
	}

	if opts.PortfolioID != "" {
		portfolioInternalID, err := s.getPortfolioInternalID(ctx, opts.PortfolioID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("re.portfolio_id = $%d", argIdx))
		args = append(args, portfolioInternalID)
		argIdx++
	}

	if opts.UserID != "" {
		userInternalID, err := s.getUserInternalID(ctx, opts.UserID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("re.user_id = $%d", argIdx))
		args = append(args, userInternalID)
		argIdx++
	}

	if opts.Status != entity.ExecutionStatusUnknown {
		whereClauses = append(whereClauses, fmt.Sprintf("re.status = $%d", argIdx))
		args = append(args, executionStatusToString(opts.Status))
		argIdx++
	}

	if opts.From != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("re.started_at >= $%d", argIdx))
		args = append(args, *opts.From)
		argIdx++
	}

	if opts.To != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("re.started_at <= $%d", argIdx))
		args = append(args, *opts.To)
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("re.uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT re.uuid, r.uuid, p.uuid, u.uuid, re.status, re.error_message,
		       re.created_transaction_ids, re.affected_holding_ids, re.transactions_created,
		       re.execution_summary, re.started_at, re.completed_at
		FROM rule_executions re
		JOIN rules r ON re.rule_id = r.id
		LEFT JOIN portfolios p ON re.portfolio_id = p.id
		LEFT JOIN users u ON re.user_id = u.id
		%s
		ORDER BY re.uuid
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list rule executions: %w", err)
	}
	defer rows.Close()

	executions := make([]*entity.RuleExecution, 0, limit)
	for rows.Next() {
		var e entity.RuleExecution
		var portfolioID *string
		var userID *string
		var statusStr string
		var errorMessage *string
		var txIDsJSON []byte
		var holdingIDsJSON []byte
		var summaryJSON []byte

		if err := rows.Scan(
			&e.ID,
			&e.RuleID,
			&portfolioID,
			&userID,
			&statusStr,
			&errorMessage,
			&txIDsJSON,
			&holdingIDsJSON,
			&e.TransactionsCreated,
			&summaryJSON,
			&e.StartedAt,
			&e.CompletedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan rule execution: %w", err)
		}

		if portfolioID != nil {
			e.PortfolioID = *portfolioID
		}
		if userID != nil {
			e.UserID = *userID
		}
		if errorMessage != nil {
			e.ErrorMessage = *errorMessage
		}
		e.Status = stringToExecutionStatus(statusStr)

		if err := json.Unmarshal(txIDsJSON, &e.CreatedTransactionIDs); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal created_transaction_ids: %w", err)
		}
		if err := json.Unmarshal(holdingIDsJSON, &e.AffectedHoldingIDs); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal affected_holding_ids: %w", err)
		}
		if len(summaryJSON) > 0 {
			e.ExecutionSummary = json.RawMessage(summaryJSON)
		}

		executions = append(executions, &e)
	}

	var nextPageToken string
	if len(executions) > limit {
		lastItem := executions[limit-1]
		executions = executions[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return executions, nextPageToken, nil
}

// --- Helper methods ---

func (s *AutomationStore) getUserInternalID(ctx context.Context, id string) (int64, error) {
	if !isValidUUID(id) {
		return 0, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	var internalID int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM users WHERE uuid = $1", id).Scan(&internalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: user not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get user: %w", err)
	}
	return internalID, nil
}

func (s *AutomationStore) getPortfolioInternalID(ctx context.Context, id string) (int64, error) {
	if !isValidUUID(id) {
		return 0, fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
	}

	var internalID int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM portfolios WHERE uuid = $1", id).Scan(&internalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: portfolio not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get portfolio: %w", err)
	}
	return internalID, nil
}

func (s *AutomationStore) getRuleInternalID(ctx context.Context, id string) (int64, error) {
	if !isValidUUID(id) {
		return 0, fmt.Errorf("%w: invalid rule ID format", store.ErrInvalidArgument)
	}

	var internalID int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM rules WHERE uuid = $1", id).Scan(&internalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: rule not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get rule: %w", err)
	}
	return internalID, nil
}

// --- Status converters ---

func ruleStatusToString(s entity.RuleStatus) string {
	switch s {
	case entity.RuleStatusActive:
		return "active"
	case entity.RuleStatusPaused:
		return "paused"
	case entity.RuleStatusDisabled:
		return "disabled"
	case entity.RuleStatusError:
		return "error"
	default:
		return "unknown"
	}
}

func stringToRuleStatus(s string) entity.RuleStatus {
	switch s {
	case "active":
		return entity.RuleStatusActive
	case "paused":
		return entity.RuleStatusPaused
	case "disabled":
		return entity.RuleStatusDisabled
	case "error":
		return entity.RuleStatusError
	default:
		return entity.RuleStatusUnknown
	}
}

func executionStatusToString(s entity.ExecutionStatus) string {
	switch s {
	case entity.ExecutionStatusPending:
		return "pending"
	case entity.ExecutionStatusInProgress:
		return "in_progress"
	case entity.ExecutionStatusCompleted:
		return "completed"
	case entity.ExecutionStatusFailed:
		return "failed"
	case entity.ExecutionStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func stringToExecutionStatus(s string) entity.ExecutionStatus {
	switch s {
	case "pending":
		return entity.ExecutionStatusPending
	case "in_progress":
		return entity.ExecutionStatusInProgress
	case "completed":
		return entity.ExecutionStatusCompleted
	case "failed":
		return entity.ExecutionStatusFailed
	case "cancelled":
		return entity.ExecutionStatusCancelled
	default:
		return entity.ExecutionStatusUnknown
	}
}

// --- JSON helpers for schedule ---

type scheduleJSON struct {
	CronExpression string     `json:"cron_expression,omitempty"`
	Timezone       string     `json:"timezone,omitempty"`
	OneTime        bool       `json:"one_time,omitempty"`
	ExecuteAfter   *time.Time `json:"execute_after,omitempty"`
}

func marshalSchedule(s *entity.RuleSchedule) ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	data, err := json.Marshal(scheduleJSON{
		CronExpression: s.CronExpression,
		Timezone:       s.Timezone,
		OneTime:        s.OneTime,
		ExecuteAfter:   s.ExecuteAfter,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schedule: %w", err)
	}
	return data, nil
}

func unmarshalSchedule(data []byte) (*entity.RuleSchedule, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s scheduleJSON
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &entity.RuleSchedule{
		CronExpression: s.CronExpression,
		Timezone:       s.Timezone,
		OneTime:        s.OneTime,
		ExecuteAfter:   s.ExecuteAfter,
	}, nil
}
