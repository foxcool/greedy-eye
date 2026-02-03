package telegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTelegramClient_SendMessage(t *testing.T) {
	client := NewClient("test-bot-token")

	t.Run("should return unimplemented error", func(t *testing.T) {
		err := client.SendMessage(context.Background(), "123456", "test message", nil)

		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestTelegramClient_SendMessageWithKeyboard(t *testing.T) {
	client := NewClient("test-bot-token")

	t.Run("should return unimplemented error", func(t *testing.T) {
		err := client.SendMessageWithKeyboard(context.Background(), "123456", "test message", nil)

		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestTelegramClient_SendPhoto(t *testing.T) {
	client := NewClient("test-bot-token")

	t.Run("should return unimplemented error", func(t *testing.T) {
		err := client.SendPhoto(context.Background(), "123456", "https://example.com/photo.jpg", "caption")

		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestTelegramClient_GetMe(t *testing.T) {
	client := NewClient("test-bot-token")

	t.Run("should return unimplemented error", func(t *testing.T) {
		result, err := client.GetMe(context.Background())

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}
