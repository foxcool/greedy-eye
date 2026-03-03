//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertTestUser inserts a user directly into the DB and returns the UUID.
func insertTestUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	userUUID := uuid.New().String()
	prefs, _ := json.Marshal(map[string]any{})
	_, err := pool.Exec(context.Background(),
		"INSERT INTO users (uuid, email, name, preferences, created_at, updated_at) VALUES ($1, $2, $3, $4, NOW(), NOW())",
		userUUID, userUUID+"@test.com", "Test User", prefs,
	)
	require.NoError(t, err)
	return userUUID
}

// insertTestPortfolio inserts a portfolio and returns the UUID.
func insertTestPortfolio(t *testing.T, pool *pgxpool.Pool, userUUID string) string {
	t.Helper()
	portfolioUUID := uuid.New().String()
	data, _ := json.Marshal(map[string]any{})
	_, err := pool.Exec(context.Background(),
		`INSERT INTO portfolios (uuid, user_id, name, data, created_at, updated_at)
		 VALUES ($1, (SELECT id FROM users WHERE uuid=$2), $3, $4, NOW(), NOW())`,
		portfolioUUID, userUUID, "Test Portfolio", data,
	)
	require.NoError(t, err)
	return portfolioUUID
}

// createTestAutomationRule creates a rule via the AutomationStore.
func createTestAutomationRule(t *testing.T, s *AutomationStore, portfolioID, userID string) *entity.Rule {
	t.Helper()
	rule := &entity.Rule{
		Name:          "Test Rule",
		RuleType:      "dca",
		PortfolioID:   portfolioID,
		UserID:        userID,
		Status:        entity.RuleStatusActive,
		Configuration: json.RawMessage(`{"amount": 100}`),
	}
	created, err := s.CreateRule(context.Background(), rule)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.NotEmpty(t, created.ID)
	assert.NotZero(t, created.CreatedAt)
	return created
}

// createTestAutomationExecution creates a rule execution via the AutomationStore.
func createTestAutomationExecution(t *testing.T, s *AutomationStore, ruleID string) *entity.RuleExecution {
	t.Helper()
	exec := &entity.RuleExecution{
		RuleID:    ruleID,
		Status:    entity.ExecutionStatusPending,
		StartedAt: time.Now(),
	}
	created, err := s.CreateRuleExecution(context.Background(), exec)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.NotEmpty(t, created.ID)
	return created
}

// --- Rules tests ---

func TestCreateRule(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)

	t.Run("Valid rule creation", func(t *testing.T) {
		rule := &entity.Rule{
			Name:          "DCA Rule",
			RuleType:      "dca",
			PortfolioID:   portfolioID,
			UserID:        userID,
			Status:        entity.RuleStatusActive,
			Configuration: json.RawMessage(`{"amount": 100}`),
		}
		created, err := s.CreateRule(context.Background(), rule)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, rule.Name, created.Name)
		assert.Equal(t, rule.RuleType, created.RuleType)
		assert.Equal(t, rule.PortfolioID, created.PortfolioID)
		assert.Equal(t, rule.UserID, created.UserID)
		assert.Equal(t, rule.Status, created.Status)
		assert.NotZero(t, created.CreatedAt)
		assert.NotZero(t, created.UpdatedAt)
	})

	t.Run("With schedule", func(t *testing.T) {
		after := time.Now().Add(time.Hour)
		rule := &entity.Rule{
			Name:          "Scheduled Rule",
			RuleType:      "rebalancing",
			PortfolioID:   portfolioID,
			UserID:        userID,
			Status:        entity.RuleStatusActive,
			Configuration: json.RawMessage(`{}`),
			Schedule: &entity.RuleSchedule{
				CronExpression: "0 9 * * *",
				Timezone:       "UTC",
				ExecuteAfter:   &after,
			},
		}
		created, err := s.CreateRule(context.Background(), rule)
		require.NoError(t, err)
		require.NotNil(t, created.Schedule)
		assert.Equal(t, "0 9 * * *", created.Schedule.CronExpression)
		assert.Equal(t, "UTC", created.Schedule.Timezone)
	})

	t.Run("nil rule", func(t *testing.T) {
		_, err := s.CreateRule(context.Background(), nil)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Missing name", func(t *testing.T) {
		rule := &entity.Rule{
			RuleType:    "dca",
			PortfolioID: portfolioID,
			UserID:      userID,
		}
		_, err := s.CreateRule(context.Background(), rule)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Missing user_id", func(t *testing.T) {
		rule := &entity.Rule{
			Name:        "No User",
			RuleType:    "dca",
			PortfolioID: portfolioID,
		}
		_, err := s.CreateRule(context.Background(), rule)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Missing portfolio_id", func(t *testing.T) {
		rule := &entity.Rule{
			Name:     "No Portfolio",
			RuleType: "dca",
			UserID:   userID,
		}
		_, err := s.CreateRule(context.Background(), rule)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Non-existent user", func(t *testing.T) {
		rule := &entity.Rule{
			Name:          "Ghost User",
			RuleType:      "dca",
			PortfolioID:   portfolioID,
			UserID:        uuid.New().String(),
			Configuration: json.RawMessage(`{}`),
		}
		_, err := s.CreateRule(context.Background(), rule)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Non-existent portfolio", func(t *testing.T) {
		rule := &entity.Rule{
			Name:          "Ghost Portfolio",
			RuleType:      "dca",
			PortfolioID:   uuid.New().String(),
			UserID:        userID,
			Configuration: json.RawMessage(`{}`),
		}
		_, err := s.CreateRule(context.Background(), rule)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestGetRule(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule := createTestAutomationRule(t, s, portfolioID, userID)

	t.Run("Get existing rule", func(t *testing.T) {
		res, err := s.GetRule(context.Background(), rule.ID)
		require.NoError(t, err)
		assert.Equal(t, rule.ID, res.ID)
		assert.Equal(t, rule.Name, res.Name)
		assert.Equal(t, rule.RuleType, res.RuleType)
		assert.Equal(t, rule.PortfolioID, res.PortfolioID)
		assert.Equal(t, rule.UserID, res.UserID)
		assert.Equal(t, rule.Status, res.Status)
	})

	t.Run("Get non-existent rule", func(t *testing.T) {
		_, err := s.GetRule(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Empty ID", func(t *testing.T) {
		_, err := s.GetRule(context.Background(), "")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Invalid UUID format", func(t *testing.T) {
		_, err := s.GetRule(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestUpdateRule(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule := createTestAutomationRule(t, s, portfolioID, userID)

	t.Run("Update name", func(t *testing.T) {
		updated := &entity.Rule{
			ID:   rule.ID,
			Name: "Updated Name",
		}
		res, err := s.UpdateRule(context.Background(), updated, []string{"name"})
		require.NoError(t, err)
		assert.Equal(t, "Updated Name", res.Name)
		assert.Equal(t, rule.RuleType, res.RuleType)
	})

	t.Run("Update status", func(t *testing.T) {
		updated := &entity.Rule{
			ID:     rule.ID,
			Status: entity.RuleStatusPaused,
		}
		res, err := s.UpdateRule(context.Background(), updated, []string{"status"})
		require.NoError(t, err)
		assert.Equal(t, entity.RuleStatusPaused, res.Status)
	})

	t.Run("Update description", func(t *testing.T) {
		updated := &entity.Rule{
			ID:          rule.ID,
			Description: "New description",
		}
		res, err := s.UpdateRule(context.Background(), updated, []string{"description"})
		require.NoError(t, err)
		assert.Equal(t, "New description", res.Description)
	})

	t.Run("Update non-existent rule", func(t *testing.T) {
		updated := &entity.Rule{
			ID:   uuid.New().String(),
			Name: "Ghost",
		}
		_, err := s.UpdateRule(context.Background(), updated, []string{"name"})
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Missing ID", func(t *testing.T) {
		_, err := s.UpdateRule(context.Background(), &entity.Rule{Name: "No ID"}, []string{"name"})
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestDeleteRule(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule := createTestAutomationRule(t, s, portfolioID, userID)

	t.Run("Delete existing rule", func(t *testing.T) {
		err := s.DeleteRule(context.Background(), rule.ID)
		assert.NoError(t, err)
		_, err = s.GetRule(context.Background(), rule.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existent rule", func(t *testing.T) {
		err := s.DeleteRule(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Invalid UUID", func(t *testing.T) {
		err := s.DeleteRule(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestListRules(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)

	// Create several DCA rules
	for i := 0; i < 3; i++ {
		createTestAutomationRule(t, s, portfolioID, userID)
	}

	// Create one paused rebalancing rule
	paused := &entity.Rule{
		Name:          "Paused Rule",
		RuleType:      "rebalancing",
		PortfolioID:   portfolioID,
		UserID:        userID,
		Status:        entity.RuleStatusPaused,
		Configuration: json.RawMessage(`{}`),
	}
	_, err := s.CreateRule(context.Background(), paused)
	require.NoError(t, err)

	t.Run("List all", func(t *testing.T) {
		res, _, err := s.ListRules(context.Background(), automation.ListRulesOpts{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 4)
	})

	t.Run("Filter by user", func(t *testing.T) {
		res, _, err := s.ListRules(context.Background(), automation.ListRulesOpts{
			UserID: userID,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 4)
		for _, r := range res {
			assert.Equal(t, userID, r.UserID)
		}
	})

	t.Run("Filter by portfolio", func(t *testing.T) {
		res, _, err := s.ListRules(context.Background(), automation.ListRulesOpts{
			PortfolioID: portfolioID,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 4)
	})

	t.Run("Filter by status paused", func(t *testing.T) {
		res, _, err := s.ListRules(context.Background(), automation.ListRulesOpts{
			Status: entity.RuleStatusPaused,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 1)
		for _, r := range res {
			assert.Equal(t, entity.RuleStatusPaused, r.Status)
		}
	})

	t.Run("Filter by rule_type", func(t *testing.T) {
		res, _, err := s.ListRules(context.Background(), automation.ListRulesOpts{
			RuleType: "rebalancing",
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 1)
		for _, r := range res {
			assert.Equal(t, "rebalancing", r.RuleType)
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		res, nextToken, err := s.ListRules(context.Background(), automation.ListRulesOpts{
			PageSize: 2,
		})
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.NotEmpty(t, nextToken)

		res2, _, err := s.ListRules(context.Background(), automation.ListRulesOpts{
			PageSize:  2,
			PageToken: nextToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res2)
		// Verify no overlap
		ids1 := make(map[string]bool)
		for _, r := range res {
			ids1[r.ID] = true
		}
		for _, r := range res2 {
			assert.False(t, ids1[r.ID], "pagination returned duplicate rule")
		}
	})
}

// --- RuleExecution tests ---

func TestCreateRuleExecution(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule := createTestAutomationRule(t, s, portfolioID, userID)

	t.Run("Minimal execution", func(t *testing.T) {
		exec := &entity.RuleExecution{
			RuleID:    rule.ID,
			Status:    entity.ExecutionStatusPending,
			StartedAt: time.Now(),
		}
		created, err := s.CreateRuleExecution(context.Background(), exec)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, rule.ID, created.RuleID)
		assert.Equal(t, entity.ExecutionStatusPending, created.Status)
	})

	t.Run("Full execution with optional fields", func(t *testing.T) {
		now := time.Now()
		completedAt := now.Add(time.Minute)
		exec := &entity.RuleExecution{
			RuleID:                rule.ID,
			PortfolioID:           portfolioID,
			UserID:                userID,
			Status:                entity.ExecutionStatusCompleted,
			StartedAt:             now,
			CompletedAt:           &completedAt,
			CreatedTransactionIDs: []string{"tx1", "tx2"},
			AffectedHoldingIDs:    []string{"h1"},
			TransactionsCreated:   2,
			ExecutionSummary:      json.RawMessage(`{"success": true}`),
		}
		created, err := s.CreateRuleExecution(context.Background(), exec)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, portfolioID, created.PortfolioID)
		assert.Equal(t, userID, created.UserID)
		assert.Equal(t, entity.ExecutionStatusCompleted, created.Status)
		assert.ElementsMatch(t, []string{"tx1", "tx2"}, created.CreatedTransactionIDs)
		assert.ElementsMatch(t, []string{"h1"}, created.AffectedHoldingIDs)
		assert.EqualValues(t, 2, created.TransactionsCreated)
	})

	t.Run("nil execution", func(t *testing.T) {
		_, err := s.CreateRuleExecution(context.Background(), nil)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Missing rule_id", func(t *testing.T) {
		exec := &entity.RuleExecution{
			Status:    entity.ExecutionStatusPending,
			StartedAt: time.Now(),
		}
		_, err := s.CreateRuleExecution(context.Background(), exec)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Non-existent rule_id", func(t *testing.T) {
		exec := &entity.RuleExecution{
			RuleID:    uuid.New().String(),
			Status:    entity.ExecutionStatusPending,
			StartedAt: time.Now(),
		}
		_, err := s.CreateRuleExecution(context.Background(), exec)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestGetRuleExecution(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule := createTestAutomationRule(t, s, portfolioID, userID)
	exec := createTestAutomationExecution(t, s, rule.ID)

	t.Run("Get existing execution", func(t *testing.T) {
		res, err := s.GetRuleExecution(context.Background(), exec.ID)
		require.NoError(t, err)
		assert.Equal(t, exec.ID, res.ID)
		assert.Equal(t, rule.ID, res.RuleID)
		assert.Equal(t, entity.ExecutionStatusPending, res.Status)
	})

	t.Run("Get non-existent execution", func(t *testing.T) {
		_, err := s.GetRuleExecution(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Empty ID", func(t *testing.T) {
		_, err := s.GetRuleExecution(context.Background(), "")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Invalid UUID", func(t *testing.T) {
		_, err := s.GetRuleExecution(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestUpdateRuleExecution(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule := createTestAutomationRule(t, s, portfolioID, userID)
	exec := createTestAutomationExecution(t, s, rule.ID)

	t.Run("Update status to in_progress", func(t *testing.T) {
		updated := &entity.RuleExecution{
			ID:     exec.ID,
			Status: entity.ExecutionStatusInProgress,
		}
		res, err := s.UpdateRuleExecution(context.Background(), updated, []string{"status"})
		require.NoError(t, err)
		assert.Equal(t, entity.ExecutionStatusInProgress, res.Status)
	})

	t.Run("Update completed_at", func(t *testing.T) {
		completedAt := time.Now()
		updated := &entity.RuleExecution{
			ID:          exec.ID,
			Status:      entity.ExecutionStatusCompleted,
			CompletedAt: &completedAt,
		}
		res, err := s.UpdateRuleExecution(context.Background(), updated, []string{"status", "completed_at"})
		require.NoError(t, err)
		assert.Equal(t, entity.ExecutionStatusCompleted, res.Status)
		assert.NotNil(t, res.CompletedAt)
	})

	t.Run("Update error_message", func(t *testing.T) {
		updated := &entity.RuleExecution{
			ID:           exec.ID,
			Status:       entity.ExecutionStatusFailed,
			ErrorMessage: "something went wrong",
		}
		res, err := s.UpdateRuleExecution(context.Background(), updated, []string{"status", "error_message"})
		require.NoError(t, err)
		assert.Equal(t, "something went wrong", res.ErrorMessage)
		assert.Equal(t, entity.ExecutionStatusFailed, res.Status)
	})

	t.Run("Update non-existent execution", func(t *testing.T) {
		updated := &entity.RuleExecution{
			ID:     uuid.New().String(),
			Status: entity.ExecutionStatusCompleted,
		}
		_, err := s.UpdateRuleExecution(context.Background(), updated, []string{"status"})
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Missing ID", func(t *testing.T) {
		_, err := s.UpdateRuleExecution(context.Background(), &entity.RuleExecution{}, []string{"status"})
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestListRuleExecutions(t *testing.T) {
	pool := getTestPool(t)
	s := NewAutomationStore(pool)
	userID := insertTestUser(t, pool)
	portfolioID := insertTestPortfolio(t, pool, userID)
	rule1 := createTestAutomationRule(t, s, portfolioID, userID)
	rule2 := createTestAutomationRule(t, s, portfolioID, userID)

	// Create 3 pending executions for rule1
	for i := 0; i < 3; i++ {
		createTestAutomationExecution(t, s, rule1.ID)
	}

	// Create one completed execution for rule2
	completedAt := time.Now()
	exec := &entity.RuleExecution{
		RuleID:      rule2.ID,
		Status:      entity.ExecutionStatusCompleted,
		StartedAt:   time.Now(),
		CompletedAt: &completedAt,
	}
	_, err := s.CreateRuleExecution(context.Background(), exec)
	require.NoError(t, err)

	t.Run("List all", func(t *testing.T) {
		res, _, err := s.ListRuleExecutions(context.Background(), automation.ListRuleExecutionsOpts{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 4)
	})

	t.Run("Filter by rule_id", func(t *testing.T) {
		res, _, err := s.ListRuleExecutions(context.Background(), automation.ListRuleExecutionsOpts{
			RuleID: rule1.ID,
		})
		require.NoError(t, err)
		assert.Len(t, res, 3)
		for _, e := range res {
			assert.Equal(t, rule1.ID, e.RuleID)
		}
	})

	t.Run("Filter by status completed", func(t *testing.T) {
		res, _, err := s.ListRuleExecutions(context.Background(), automation.ListRuleExecutionsOpts{
			Status: entity.ExecutionStatusCompleted,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 1)
		for _, e := range res {
			assert.Equal(t, entity.ExecutionStatusCompleted, e.Status)
		}
	})

	t.Run("Filter by time range", func(t *testing.T) {
		from := time.Now().Add(-time.Minute)
		to := time.Now().Add(time.Minute)
		res, _, err := s.ListRuleExecutions(context.Background(), automation.ListRuleExecutionsOpts{
			From: &from,
			To:   &to,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 4)
	})

	t.Run("Pagination", func(t *testing.T) {
		res, nextToken, err := s.ListRuleExecutions(context.Background(), automation.ListRuleExecutionsOpts{
			PageSize: 2,
		})
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.NotEmpty(t, nextToken)

		res2, _, err := s.ListRuleExecutions(context.Background(), automation.ListRuleExecutionsOpts{
			PageSize:  2,
			PageToken: nextToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res2)

		ids1 := make(map[string]bool)
		for _, e := range res {
			ids1[e.ID] = true
		}
		for _, e := range res2 {
			assert.False(t, ids1[e.ID], "pagination returned duplicate execution")
		}
	})
}
