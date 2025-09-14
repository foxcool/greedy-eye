package telegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
)

func TestNewService(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)

	require.NotNil(t, service)
	assert.NotNil(t, service.log)
}

func TestService_ProcessTelegramUpdate(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.ProcessTelegramUpdateRequest{
		UpdateJson:    `{"message": {"text": "test"}}`,
		WebhookSecret: "secret123",
	}

	resp, err := service.ProcessTelegramUpdate(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ProcessTelegramUpdate not implemented")
}

func TestService_SendNotification(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.SendNotificationRequest{
		Notification: &models.TelegramNotification{
			TelegramId: "123456789",
			Type:       models.NotificationType_NOTIFICATION_TYPE_PRICE_ALERT,
			Title:      "Price Alert",
			Message:    "BTC price reached $50000",
		},
		ForceSend: false,
	}

	resp, err := service.SendNotification(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "SendNotification not implemented")
}

func TestService_SendBulkNotifications(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.SendBulkNotificationsRequest{
		Notifications: []*models.TelegramNotification{
			{
				TelegramId: "123456789",
				Type:       models.NotificationType_NOTIFICATION_TYPE_SYSTEM_ALERT,
				Title:      "System Alert",
				Message:    "System maintenance scheduled",
			},
			{
				TelegramId: "987654321",
				Type:       models.NotificationType_NOTIFICATION_TYPE_PORTFOLIO_CHANGE,
				Title:      "Portfolio Update",
				Message:    "Portfolio value changed",
			},
		},
		BatchSize: 10,
		DelayMs:   1000,
	}

	resp, err := service.SendBulkNotifications(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "SendBulkNotifications not implemented")
}

func TestService_ManageAlerts(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.ManageAlertsRequest{
		TelegramId: "123456789",
		Operation:  services.AlertOperation_ALERT_OPERATION_CREATE,
		Alert: &models.TelegramAlert{
			TelegramId:     "123456789",
			AlertType:      models.AlertType_ALERT_TYPE_PRICE_ABOVE,
			AssetSymbol:    "BTC",
			Condition:      models.AlertCondition_ALERT_CONDITION_GREATER_THAN,
			ThresholdValue: 50000,
			CustomMessage:  "BTC above $50k",
			Enabled:        true,
		},
	}

	resp, err := service.ManageAlerts(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ManageAlerts not implemented")
}

func TestService_GetUserSession(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.GetUserSessionRequest{
		TelegramId: "123456789",
	}

	resp, err := service.GetUserSession(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GetUserSession not implemented")
}

func TestService_UpdateUserSession(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.UpdateUserSessionRequest{
		Session: &models.SessionContext{
			TelegramId:       "123456789",
			CurrentOperation: "portfolio_view",
			State:            "viewing_portfolio",
			ContextData: map[string]string{
				"portfolio_id": "portfolio123",
			},
		},
	}

	resp, err := service.UpdateUserSession(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "UpdateUserSession not implemented")
}

func TestService_RegisterTelegramUser(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.RegisterTelegramUserRequest{
		TelegramUser: &models.TelegramUser{
			TelegramId:          "123456789",
			Username:            "testuser",
			FirstName:           "Test",
			LastName:            "User",
			LanguageCode:        "en",
			NotificationsEnabled: true,
		},
		LinkExistingUser: false,
		ExistingUserId:   "",
	}

	resp, err := service.RegisterTelegramUser(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "RegisterTelegramUser not implemented")
}

func TestService_GetTelegramUser(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.GetTelegramUserRequest{
		TelegramId: "123456789",
	}

	resp, err := service.GetTelegramUser(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GetTelegramUser not implemented")
}

func TestService_ProcessVoiceMessage(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.ProcessVoiceMessageRequest{
		TelegramId:      "123456789",
		AudioData:       []byte("fake_audio_data"),
		AudioFormat:     "ogg",
		DurationSeconds: 10,
		LanguageHint:    "en",
		Provider:        services.SpeechProvider_SPEECH_PROVIDER_OPENAI,
	}

	resp, err := service.ProcessVoiceMessage(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ProcessVoiceMessage not implemented")
}

func TestService_ConvertTextToSpeech(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.ConvertTextToSpeechRequest{
		Text:         "Hello, this is a test message",
		Language:     "en",
		Voice:        "alloy",
		Provider:     services.SpeechProvider_SPEECH_PROVIDER_OPENAI,
		OutputFormat: services.AudioFormat_AUDIO_FORMAT_MP3,
	}

	resp, err := service.ConvertTextToSpeech(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ConvertTextToSpeech not implemented")
}

func TestService_GetBotStats(t *testing.T) {
	log := zaptest.NewLogger(t)
	service := NewService(log)
	ctx := context.Background()

	req := &services.GetBotStatsRequest{}

	resp, err := service.GetBotStats(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GetBotStats not implemented")
}