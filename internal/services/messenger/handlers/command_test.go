package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/models"
)

func TestNewCommandHandler(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewCommandHandler(log)

	require.NotNil(t, handler)
	assert.NotNil(t, handler.log)
}

func TestCommandHandler_HandleCommand(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewCommandHandler(log)
	ctx := context.Background()

	req := &CommandRequest{
		TelegramID: "123456789",
		Command:    "/portfolio",
		Args:       []string{"overview"},
		Context: &models.SessionContext{
			TelegramId:       "123456789",
			CurrentOperation: "portfolio_view",
		},
	}

	resp, err := handler.HandleCommand(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "HandleCommand not implemented")
}

func TestCommandHandler_ValidateCommand(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewCommandHandler(log)
	ctx := context.Background()

	err := handler.ValidateCommand(ctx, "123456789", "/start")

	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ValidateCommand not implemented")
}

func TestCommandHandler_GetAvailableCommands(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewCommandHandler(log)
	ctx := context.Background()

	commands, err := handler.GetAvailableCommands(ctx, "123456789")

	assert.Nil(t, commands)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GetAvailableCommands not implemented")
}

func TestCommandHandler_FormatCommandHelp(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewCommandHandler(log)

	help, err := handler.FormatCommandHelp("/portfolio")

	assert.Empty(t, help)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "FormatCommandHelp not implemented")
}

func TestCommandRequest(t *testing.T) {
	req := &CommandRequest{
		TelegramID: "123456789",
		Command:    "/balance",
		Args:       []string{"btc", "eth"},
		Context: &models.SessionContext{
			TelegramId: "123456789",
		},
	}

	assert.Equal(t, "123456789", req.TelegramID)
	assert.Equal(t, "/balance", req.Command)
	assert.Equal(t, []string{"btc", "eth"}, req.Args)
	assert.NotNil(t, req.Context)
}

func TestCommandResponse(t *testing.T) {
	buttons := []*models.InlineButton{
		{Text: "View Details", CallbackData: "view_details"},
		{Text: "Refresh", CallbackData: "refresh"},
	}

	resp := &CommandResponse{
		Text:         "Your portfolio balance: $10,000",
		Format:       models.ResponseFormat_RESPONSE_FORMAT_MARKDOWN,
		Buttons:      buttons,
		EnableVoice:  true,
		UpdateSession: true,
		NewContext: &models.SessionContext{
			TelegramId: "123456789",
			State:      "portfolio_displayed",
		},
	}

	assert.Equal(t, "Your portfolio balance: $10,000", resp.Text)
	assert.Equal(t, models.ResponseFormat_RESPONSE_FORMAT_MARKDOWN, resp.Format)
	assert.Len(t, resp.Buttons, 2)
	assert.True(t, resp.EnableVoice)
	assert.True(t, resp.UpdateSession)
	assert.NotNil(t, resp.NewContext)
}