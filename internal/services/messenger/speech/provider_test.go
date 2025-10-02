package speech

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/services"
)

func TestNewManager(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{
		DefaultProvider:  services.SpeechProvider_SPEECH_PROVIDER_OPENAI,
		EnableFallback:   true,
		MaxAudioDuration: 120 * time.Second,
		DefaultLanguage:  "en",
		CacheTTL:        24 * time.Hour,
	}

	manager := NewManager(log, config)

	require.NotNil(t, manager)
	assert.NotNil(t, manager.log)
	assert.NotNil(t, manager.providers)
	assert.Equal(t, config, manager.config)
}

func TestManager_RegisterProvider(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{DefaultProvider: services.SpeechProvider_SPEECH_PROVIDER_OPENAI}
	manager := NewManager(log, config)

	mockProvider := &mockProvider{name: "Test Provider"}
	manager.RegisterProvider(services.SpeechProvider_SPEECH_PROVIDER_OPENAI, mockProvider)

	assert.Contains(t, manager.providers, services.SpeechProvider_SPEECH_PROVIDER_OPENAI)
}

func TestManager_SpeechToText(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{DefaultProvider: services.SpeechProvider_SPEECH_PROVIDER_OPENAI}
	manager := NewManager(log, config)
	ctx := context.Background()

	audioData := []byte("fake_audio_data")
	result, err := manager.SpeechToText(ctx, audioData, "en", services.SpeechProvider_SPEECH_PROVIDER_OPENAI)

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "SpeechToText not implemented")
}

func TestManager_TextToSpeech(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{DefaultProvider: services.SpeechProvider_SPEECH_PROVIDER_GOOGLE}
	manager := NewManager(log, config)
	ctx := context.Background()

	result, err := manager.TextToSpeech(ctx, "Hello world", "en", "alloy", services.SpeechProvider_SPEECH_PROVIDER_GOOGLE)

	assert.Nil(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "TextToSpeech not implemented")
}

func TestManager_SelectProvider(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{DefaultProvider: services.SpeechProvider_SPEECH_PROVIDER_YANDEX}
	manager := NewManager(log, config)

	provider := manager.SelectProvider("ru", 30*time.Second)

	// Stub implementation returns default provider
	assert.Equal(t, services.SpeechProvider_SPEECH_PROVIDER_YANDEX, provider)
}

func TestManager_GetAvailableProviders(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{DefaultProvider: services.SpeechProvider_SPEECH_PROVIDER_OPENAI}
	manager := NewManager(log, config)

	// Register multiple providers
	manager.RegisterProvider(services.SpeechProvider_SPEECH_PROVIDER_OPENAI, &mockProvider{})
	manager.RegisterProvider(services.SpeechProvider_SPEECH_PROVIDER_GOOGLE, &mockProvider{})

	providers := manager.GetAvailableProviders()

	assert.Len(t, providers, 2)
	assert.Contains(t, providers, services.SpeechProvider_SPEECH_PROVIDER_OPENAI)
	assert.Contains(t, providers, services.SpeechProvider_SPEECH_PROVIDER_GOOGLE)
}

func TestManager_ValidateAudio(t *testing.T) {
	log := zaptest.NewLogger(t)
	config := &Config{DefaultProvider: services.SpeechProvider_SPEECH_PROVIDER_OPENAI}
	manager := NewManager(log, config)

	audioData := []byte("test_audio_data")
	err := manager.ValidateAudio(audioData, "mp3")

	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "ValidateAudio not implemented")
}

func TestSTTResult(t *testing.T) {
	result := &STTResult{
		Text:              "Hello, this is a test",
		DetectedLanguage:  "en-US",
		Confidence:        0.95,
		ProcessingTimeMs:  1500,
	}

	assert.Equal(t, "Hello, this is a test", result.Text)
	assert.Equal(t, "en-US", result.DetectedLanguage)
	assert.Equal(t, float32(0.95), result.Confidence)
	assert.Equal(t, int32(1500), result.ProcessingTimeMs)
}

func TestTTSResult(t *testing.T) {
	result := &TTSResult{
		AudioData:        []byte("fake_audio_bytes"),
		AudioFormat:      "mp3",
		DurationSeconds:  10,
		ProcessingTimeMs: 2000,
	}

	assert.Equal(t, []byte("fake_audio_bytes"), result.AudioData)
	assert.Equal(t, "mp3", result.AudioFormat)
	assert.Equal(t, int32(10), result.DurationSeconds)
	assert.Equal(t, int32(2000), result.ProcessingTimeMs)
}

func TestConfig(t *testing.T) {
	config := &Config{
		DefaultProvider:  services.SpeechProvider_SPEECH_PROVIDER_GOOGLE,
		EnableFallback:   true,
		MaxAudioDuration: 5 * time.Minute,
		DefaultLanguage:  "ru",
		CacheTTL:        12 * time.Hour,
	}

	assert.Equal(t, services.SpeechProvider_SPEECH_PROVIDER_GOOGLE, config.DefaultProvider)
	assert.True(t, config.EnableFallback)
	assert.Equal(t, 5*time.Minute, config.MaxAudioDuration)
	assert.Equal(t, "ru", config.DefaultLanguage)
	assert.Equal(t, 12*time.Hour, config.CacheTTL)
}

// mockProvider is a test implementation of Provider interface
type mockProvider struct {
	name string
}

func (p *mockProvider) SpeechToText(ctx context.Context, audioData []byte, language string) (*STTResult, error) {
	return nil, status.Errorf(codes.Unimplemented, "mock SpeechToText")
}

func (p *mockProvider) TextToSpeech(ctx context.Context, text string, language string, voice string) (*TTSResult, error) {
	return nil, status.Errorf(codes.Unimplemented, "mock TextToSpeech")
}

func (p *mockProvider) GetSupportedLanguages() []string {
	return []string{"en", "ru"}
}

func (p *mockProvider) GetName() string {
	if p.name != "" {
		return p.name
	}
	return "Mock Provider"
}