//go:build ignore
package rule

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
)

// Service implements the RuleService gRPC interface
type Service struct {
	log *slog.Logger
}

// NewService creates a new RuleService instance
func NewService(log *slog.Logger) *Service {
	return &Service{
		log: log.With(slog.String("component", "rule")),
	}
}

// ExecuteRule executes a rule synchronously
func (s *Service) ExecuteRule(ctx context.Context, req *services.ExecuteRuleRequest) (*services.ExecuteRuleResponse, error) {
	s.log.Info("ExecuteRule called",
		slog.String("rule_id", req.RuleId),
		slog.Bool("dry_run", req.DryRun))

	return nil, status.Errorf(codes.Unimplemented, "ExecuteRule not implemented")
}

// ExecuteRuleAsync executes a rule asynchronously
func (s *Service) ExecuteRuleAsync(ctx context.Context, req *services.ExecuteRuleAsyncRequest) (*services.ExecuteRuleAsyncResponse, error) {
	s.log.Info("ExecuteRuleAsync called",
		slog.String("rule_id", req.RuleId),
		slog.Bool("dry_run", req.DryRun))

	return nil, status.Errorf(codes.Unimplemented, "ExecuteRuleAsync not implemented")
}

// CancelRuleExecution cancels a running rule execution
func (s *Service) CancelRuleExecution(ctx context.Context, req *services.CancelRuleExecutionRequest) (*emptypb.Empty, error) {
	s.log.Info("CancelRuleExecution called",
		slog.String("execution_id", req.ExecutionId),
		slog.String("reason", req.Reason))

	return nil, status.Errorf(codes.Unimplemented, "CancelRuleExecution not implemented")
}

// ValidateRule validates a rule configuration
func (s *Service) ValidateRule(ctx context.Context, req *services.ValidateRuleRequest) (*services.ValidateRuleResponse, error) {
	s.log.Info("ValidateRule called",
		slog.String("rule_type", req.Rule.RuleType),
		slog.String("rule_name", req.Rule.Name))

	return nil, status.Errorf(codes.Unimplemented, "ValidateRule not implemented")
}

// SimulateRule simulates rule execution without applying changes
func (s *Service) SimulateRule(ctx context.Context, req *services.SimulateRuleRequest) (*services.SimulateRuleResponse, error) {
	s.log.Info("SimulateRule called",
		slog.String("rule_id", req.RuleId),
		slog.Bool("include_costs", req.IncludeCosts))

	return nil, status.Errorf(codes.Unimplemented, "SimulateRule not implemented")
}

// EnableRule enables a rule
func (s *Service) EnableRule(ctx context.Context, req *services.EnableRuleRequest) (*models.Rule, error) {
	s.log.Info("EnableRule called",
		slog.String("rule_id", req.RuleId))

	return nil, status.Errorf(codes.Unimplemented, "EnableRule not implemented")
}

// DisableRule disables a rule
func (s *Service) DisableRule(ctx context.Context, req *services.DisableRuleRequest) (*models.Rule, error) {
	s.log.Info("DisableRule called",
		slog.String("rule_id", req.RuleId))

	return nil, status.Errorf(codes.Unimplemented, "DisableRule not implemented")
}

// PauseRule pauses a rule
func (s *Service) PauseRule(ctx context.Context, req *services.PauseRuleRequest) (*models.Rule, error) {
	s.log.Info("PauseRule called",
		slog.String("rule_id", req.RuleId),
		slog.String("reason", req.Reason))

	return nil, status.Errorf(codes.Unimplemented, "PauseRule not implemented")
}

// ResumeRule resumes a paused rule
func (s *Service) ResumeRule(ctx context.Context, req *services.ResumeRuleRequest) (*models.Rule, error) {
	s.log.Info("ResumeRule called",
		slog.String("rule_id", req.RuleId))

	return nil, status.Errorf(codes.Unimplemented, "ResumeRule not implemented")
}