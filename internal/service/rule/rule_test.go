//go:build ignore
package rule

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
)

func TestNewService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)

	require.NotNil(t, service)
	assert.NotNil(t, service.log)
}

func TestService_ExecuteRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.ExecuteRuleRequest{
		RuleId: "rule123",
		DryRun: true,
	}

	resp, err := service.ExecuteRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ExecuteRule not implemented")
}

func TestService_ExecuteRuleAsync(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.ExecuteRuleAsyncRequest{
		RuleId: "rule456",
		DryRun: false,
	}

	resp, err := service.ExecuteRuleAsync(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ExecuteRuleAsync not implemented")
}

func TestService_CancelRuleExecution(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.CancelRuleExecutionRequest{
		ExecutionId: "exec789",
		Reason:      "User request",
	}

	resp, err := service.CancelRuleExecution(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "CancelRuleExecution not implemented")
}

func TestService_ValidateRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.ValidateRuleRequest{
		Rule: &models.Rule{
			Id:       "rule123",
			Name:     "Test Rule",
			RuleType: "price_alert",
		},
	}

	resp, err := service.ValidateRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ValidateRule not implemented")
}

func TestService_SimulateRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.SimulateRuleRequest{
		RuleId:       "rule123",
		IncludeCosts: true,
	}

	resp, err := service.SimulateRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "SimulateRule not implemented")
}

func TestService_EnableRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.EnableRuleRequest{
		RuleId: "rule123",
	}

	resp, err := service.EnableRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "EnableRule not implemented")
}

func TestService_DisableRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.DisableRuleRequest{
		RuleId: "rule456",
	}

	resp, err := service.DisableRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "DisableRule not implemented")
}

func TestService_PauseRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.PauseRuleRequest{
		RuleId: "rule789",
		Reason: "Maintenance",
	}

	resp, err := service.PauseRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "PauseRule not implemented")
}

func TestService_ResumeRule(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(log)
	ctx := context.Background()

	req := &services.ResumeRuleRequest{
		RuleId: "rule101",
	}

	resp, err := service.ResumeRule(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ResumeRule not implemented")
}