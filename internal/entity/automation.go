package entity

import (
	"encoding/json"
	"time"
)

// RuleStatus represents the lifecycle state of a rule.
type RuleStatus int32

const (
	RuleStatusUnknown  RuleStatus = 0
	RuleStatusActive   RuleStatus = 1
	RuleStatusPaused   RuleStatus = 2
	RuleStatusDisabled RuleStatus = 3
	RuleStatusError    RuleStatus = 4
)

// ExecutionStatus represents the lifecycle state of a rule execution.
type ExecutionStatus int32

const (
	ExecutionStatusUnknown    ExecutionStatus = 0
	ExecutionStatusPending    ExecutionStatus = 1
	ExecutionStatusInProgress ExecutionStatus = 2
	ExecutionStatusCompleted  ExecutionStatus = 3
	ExecutionStatusFailed     ExecutionStatus = 4
	ExecutionStatusCancelled  ExecutionStatus = 5
)

// RuleSchedule defines when a rule should be executed.
type RuleSchedule struct {
	CronExpression string
	Timezone       string
	OneTime        bool
	ExecuteAfter   *time.Time
}

// Rule defines a business rule applied to a portfolio (DCA, rebalancing, stop-loss, etc.).
type Rule struct {
	ID            string
	Name          string
	Description   string
	RuleType      string          // e.g. "dca", "rebalancing", "stop_loss", "withdrawal"
	PortfolioID   string
	UserID        string
	Status        RuleStatus
	Configuration json.RawMessage // Rule-type-specific config
	Schedule      *RuleSchedule
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RuleExecution represents a single run of a rule.
type RuleExecution struct {
	ID                    string
	RuleID                string
	PortfolioID           string
	UserID                string
	StartedAt             time.Time
	CompletedAt           *time.Time
	Status                ExecutionStatus
	ErrorMessage          string
	CreatedTransactionIDs []string
	AffectedHoldingIDs    []string
	TransactionsCreated   int32
	ExecutionSummary      json.RawMessage
}
