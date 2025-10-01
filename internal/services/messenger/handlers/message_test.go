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

func TestNewMessageHandler(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewMessageHandler(log)

	require.NotNil(t, handler)
	assert.NotNil(t, handler.log)
}

func TestMessageHandler_HandleMessage(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewMessageHandler(log)
	ctx := context.Background()

	req := &MessageRequest{
		TelegramID: "123456789",
		Message: &models.TelegramMessage{
			MessageId:  "msg123",
			TelegramId: "123456789",
			Type:       models.MessageType_MESSAGE_TYPE_TEXT,
			Content:    "How much BTC do I have?",
			Language:   "en",
		},
		Context: &models.SessionContext{
			TelegramId: "123456789",
		},
	}

	resp, err := handler.HandleMessage(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "HandleMessage not implemented")
}

func TestMessageHandler_ParseIntent(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewMessageHandler(log)
	ctx := context.Background()

	intent, err := handler.ParseIntent(ctx, "Show me my Bitcoin balance", "en")

	assert.Nil(t, intent)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ParseIntent not implemented")
}

func TestMessageHandler_ExtractEntities(t *testing.T) {
	log := zaptest.NewLogger(t)
	handler := NewMessageHandler(log)
	ctx := context.Background()

	entities, err := handler.ExtractEntities(ctx, "Buy 0.5 BTC at $45000", "en")

	assert.Nil(t, entities)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ExtractEntities not implemented")
}

func TestMessageRequest(t *testing.T) {
	message := &models.TelegramMessage{
		MessageId:  "msg456",
		TelegramId: "987654321",
		Type:       models.MessageType_MESSAGE_TYPE_VOICE,
		Content:    "/tmp/voice_file.ogg",
		Language:   "ru",
	}

	req := &MessageRequest{
		TelegramID: "987654321",
		Message:    message,
		Context: &models.SessionContext{
			TelegramId:       "987654321",
			CurrentOperation: "voice_processing",
		},
	}

	assert.Equal(t, "987654321", req.TelegramID)
	assert.Equal(t, message, req.Message)
	assert.NotNil(t, req.Context)
	assert.Equal(t, "voice_processing", req.Context.CurrentOperation)
}

func TestMessageResponse(t *testing.T) {
	response := &models.TelegramResponse{
		TelegramId: "123456789",
		Type:       models.ResponseType_RESPONSE_TYPE_TEXT,
		Content:    "Your BTC balance is 0.5 BTC",
		Format:     models.ResponseFormat_RESPONSE_FORMAT_MARKDOWN,
	}

	resp := &MessageResponse{
		Response:      response,
		UpdateSession: true,
		NewContext: &models.SessionContext{
			TelegramId: "123456789",
			State:      "balance_displayed",
		},
	}

	assert.Equal(t, response, resp.Response)
	assert.True(t, resp.UpdateSession)
	assert.NotNil(t, resp.NewContext)
}

func TestIntent(t *testing.T) {
	entities := []*Entity{
		{Type: "asset", Value: "BTC", Score: 0.95},
		{Type: "amount", Value: "0.5", Score: 0.89},
	}

	intent := &Intent{
		Type:       "balance_inquiry",
		Confidence: 0.92,
		Entities:   entities,
		Context: map[string]string{
			"user_intent": "check_balance",
			"asset_type":  "cryptocurrency",
		},
	}

	assert.Equal(t, "balance_inquiry", intent.Type)
	assert.Equal(t, 0.92, intent.Confidence)
	assert.Len(t, intent.Entities, 2)
	assert.Equal(t, "BTC", intent.Entities[0].Value)
	assert.Equal(t, "check_balance", intent.Context["user_intent"])
}

func TestEntity(t *testing.T) {
	entity := &Entity{
		Type:  "date",
		Value: "2024-01-15",
		Score: 0.88,
	}

	assert.Equal(t, "date", entity.Type)
	assert.Equal(t, "2024-01-15", entity.Value)
	assert.Equal(t, 0.88, entity.Score)
}