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

func TestNewYandexProvider(t *testing.T) {
	log := zaptest.NewLogger(t)
	apiKey := "test_yandex_key"
	folderID := "test_folder_id"
	
	provider := NewYandexProvider(log, apiKey, folderID)

	require.NotNil(t, provider)
	assert.NotNil(t, provider.log)
	assert.Equal(t, apiKey, provider.apiKey)
	assert.Equal(t, folderID, provider.folderID)
}

func TestYandexProvider_SpeechToText(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewYandexProvider(log, "test_key", "test_folder")
	ctx := context.Background()

	audioData := []byte("fake_audio_data")
	result, err := provider.SpeechToText(ctx, audioData, "ru-RU")

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "YandexProvider SpeechToText not implemented")
}

func TestYandexProvider_TextToSpeech(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewYandexProvider(log, "test_key", "test_folder")
	ctx := context.Background()

	result, err := provider.TextToSpeech(ctx, "Привет мир", "ru-RU", "jane")

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "YandexProvider TextToSpeech not implemented")
}

func TestYandexProvider_GetSupportedLanguages(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewYandexProvider(log, "test_key", "test_folder")

	languages := provider.GetSupportedLanguages()

	expectedLanguages := []string{"ru-RU", "en-US", "tr-TR", "kk-KK", "uz-UZ", "he-IL"}
	assert.Equal(t, expectedLanguages, languages)
	assert.Contains(t, languages, "ru-RU")
	assert.Contains(t, languages, "en-US")
	assert.Len(t, languages, 6)
}

func TestYandexProvider_GetName(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewYandexProvider(log, "test_key", "test_folder")

	name := provider.GetName()

	assert.Equal(t, "Yandex SpeechKit", name)
}

func TestYandexProvider_Interface(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewYandexProvider(log, "test_key", "test_folder")

	// Ensure YandexProvider implements Provider interface
	var _ Provider = provider
}

func TestYandexProvider_RussianSupport(t *testing.T) {
	log := zaptest.NewLogger(t)
	provider := NewYandexProvider(log, "test_key", "test_folder")

	languages := provider.GetSupportedLanguages()

	// Yandex is primarily focused on Russian and CIS countries
	assert.Contains(t, languages, "ru-RU", "Russian should be supported")
	assert.Contains(t, languages, "kk-KK", "Kazakh should be supported")
	assert.Contains(t, languages, "uz-UZ", "Uzbek should be supported")
	
	// Should also support English
	assert.Contains(t, languages, "en-US", "English should be supported")
}