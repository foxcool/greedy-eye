package speech

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewGoogleProvider(t *testing.T) {
	log := zaptest.NewLogger(t)
	apiKey := "test_api_key"
	projectID := "test_project"
	
	provider := NewGoogleProvider(log, apiKey, projectID)

	require.NotNil(t, provider)
	assert.NotNil(t, provider.log)
	assert.Equal(t, apiKey, provider.apiKey)
	assert.Equal(t, projectID, provider.projectID)
}

func TestGoogleProvider_SpeechToText(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewGoogleProvider(log, "test_key", "test_project")
	ctx := context.Background()

	audioData := []byte("fake_audio_data")
	result, err := provider.SpeechToText(ctx, audioData, "en-US")

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GoogleProvider SpeechToText not implemented")
}

func TestGoogleProvider_TextToSpeech(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewGoogleProvider(log, "test_key", "test_project")
	ctx := context.Background()

	result, err := provider.TextToSpeech(ctx, "Hello world", "en-US", "en-US-Standard-A")

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "GoogleProvider TextToSpeech not implemented")
}

func TestGoogleProvider_GetSupportedLanguages(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewGoogleProvider(log, "test_key", "test_project")

	languages := provider.GetSupportedLanguages()

	expectedLanguages := []string{"en-US", "ru-RU", "en-GB", "de-DE", "fr-FR", "es-ES", "it-IT", "ja-JP", "ko-KR", "zh-CN"}
	assert.Equal(t, expectedLanguages, languages)
	assert.Contains(t, languages, "en-US")
	assert.Contains(t, languages, "ru-RU")
	assert.Len(t, languages, 10)
}

func TestGoogleProvider_GetName(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewGoogleProvider(log, "test_key", "test_project")

	name := provider.GetName()

	assert.Equal(t, "Google Cloud Speech", name)
}

func TestGoogleProvider_Interface(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewGoogleProvider(log, "test_key", "test_project")

	// Ensure GoogleProvider implements Provider interface
	var _ Provider = provider
}