package context

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/models"
)

func TestNewSessionManager(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)

	require.NotNil(t, manager)
	assert.NotNil(t, manager.log)
}

func TestSessionManager_GetSession(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)
	ctx := context.Background()

	session, err := manager.GetSession(ctx, "123456789")

	assert.Nil(t, session)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GetSession not implemented")
}

func TestSessionManager_CreateSession(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)
	ctx := context.Background()

	session, err := manager.CreateSession(ctx, "123456789", "portfolio_view")

	assert.Nil(t, session)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "CreateSession not implemented")
}

func TestSessionManager_UpdateSession(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)
	ctx := context.Background()

	session := &models.SessionContext{
		TelegramId:       "123456789",
		CurrentOperation: "balance_check",
		State:            "processing",
		ContextData: map[string]string{
			"last_command": "/balance",
		},
	}

	err := manager.UpdateSession(ctx, session)

	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "UpdateSession not implemented")
}

func TestSessionManager_DeleteSession(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)
	ctx := context.Background()

	err := manager.DeleteSession(ctx, "123456789")

	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "DeleteSession not implemented")
}

func TestSessionManager_IsSessionExpired(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)

	session := &models.SessionContext{
		TelegramId: "123456789",
	}

	// Stub implementation always returns false
	expired := manager.IsSessionExpired(session)
	assert.False(t, expired)
}

func TestSessionManager_CleanupExpiredSessions(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)
	ctx := context.Background()

	count, err := manager.CleanupExpiredSessions(ctx)

	assert.Equal(t, 0, count)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "CleanupExpiredSessions not implemented")
}

func TestSessionManager_AddToHistory(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)

	session := &models.SessionContext{
		TelegramId:          "123456789",
		ConversationHistory: []string{"previous message"},
	}

	// This method is void, so we just call it to ensure no panic
	manager.AddToHistory(session, "new message", 10)

	// Stub implementation doesn't modify the session
	assert.Len(t, session.ConversationHistory, 1)
}

func TestSessionManager_GetContextData(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)

	session := &models.SessionContext{
		TelegramId: "123456789",
		ContextData: map[string]string{
			"key1": "value1",
		},
	}

	// Stub implementation always returns empty
	value, exists := manager.GetContextData(session, "key1")
	assert.Empty(t, value)
	assert.False(t, exists)
}

func TestSessionManager_SetContextData(t *testing.T) {
	log := zaptest.NewLogger(t)
	manager := NewSessionManager(log)

	session := &models.SessionContext{
		TelegramId: "123456789",
		ContextData: map[string]string{},
	}

	// This method is void, so we just call it to ensure no panic
	manager.SetContextData(session, "test_key", "test_value")

	// Stub implementation doesn't modify the session
	assert.Empty(t, session.ContextData)
}

func TestSessionConfig(t *testing.T) {
	config := &SessionConfig{
		DefaultTimeout:   30 * time.Minute,
		MaxHistoryLength: 50,
		StorageType:      "redis",
		RedisURL:         "redis://localhost:6379",
	}

	assert.Equal(t, 30*time.Minute, config.DefaultTimeout)
	assert.Equal(t, 50, config.MaxHistoryLength)
	assert.Equal(t, "redis", config.StorageType)
	assert.Equal(t, "redis://localhost:6379", config.RedisURL)
}