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

func TestNewOpenAIProvider(t *testing.T) {
	log := zaptest.NewLogger(t)
	apiKey := "test_openai_key"
	
	provider := NewOpenAIProvider(log, apiKey)

	require.NotNil(t, provider)
	assert.NotNil(t, provider.log)
	assert.Equal(t, apiKey, provider.apiKey)
}

func TestOpenAIProvider_SpeechToText(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewOpenAIProvider(log, "test_key")
	ctx := context.Background()

	audioData := []byte("fake_audio_data")
	result, err := provider.SpeechToText(ctx, audioData, "en")

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "OpenAIProvider SpeechToText not implemented")
}

func TestOpenAIProvider_TextToSpeech(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewOpenAIProvider(log, "test_key")
	ctx := context.Background()

	result, err := provider.TextToSpeech(ctx, "Hello world", "en", "alloy")

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "OpenAIProvider TextToSpeech not implemented")
}

func TestOpenAIProvider_GetSupportedLanguages(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewOpenAIProvider(log, "test_key")

	languages := provider.GetSupportedLanguages()

	// OpenAI Whisper supports 100+ languages, verify some key ones
	assert.Contains(t, languages, "en")
	assert.Contains(t, languages, "ru")
	assert.Contains(t, languages, "zh")
	assert.Contains(t, languages, "es")
	assert.Contains(t, languages, "fr")
	assert.Contains(t, languages, "de")
	assert.Contains(t, languages, "ja")
	assert.Contains(t, languages, "ko")
	
	// Check that it has many languages (Whisper supports 57+ languages)
	assert.Greater(t, len(languages), 50)
}

func TestOpenAIProvider_GetName(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewOpenAIProvider(log, "test_key")

	name := provider.GetName()

	assert.Equal(t, "OpenAI Whisper", name)
}

func TestOpenAIProvider_Interface(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewOpenAIProvider(log, "test_key")

	// Ensure OpenAIProvider implements Provider interface
	var _ Provider = provider
}

func TestOpenAIProvider_SupportedLanguagesContent(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewOpenAIProvider(log, "test_key")

	languages := provider.GetSupportedLanguages()

	// Test specific languages that should be supported
	expectedLanguages := []string{
		"af", "ar", "hy", "az", "be", "bs", "bg", "ca", "zh", "hr", "cs", "da", "nl", "en", "et", "fi", "fr",
		"gl", "de", "el", "he", "hi", "hu", "is", "id", "it", "ja", "kn", "kk", "ko", "lv", "lt", "mk", "ms",
		"mr", "mi", "ne", "no", "fa", "pl", "pt", "ro", "ru", "sr", "sk", "sl", "es", "sw", "sv", "tl", "ta",
		"th", "tr", "uk", "ur", "vi", "cy", "yi",
	}

	for _, lang := range expectedLanguages {
		assert.Contains(t, languages, lang, "Language %s should be supported by OpenAI Whisper", lang)
	}
}