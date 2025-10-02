package speech

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/services"
)

// Provider interface defines speech processing capabilities
type Provider interface {
	SpeechToText(ctx context.Context, audioData []byte, language string) (*STTResult, error)
	TextToSpeech(ctx context.Context, text string, language string, voice string) (*TTSResult, error)
	GetSupportedLanguages() []string
	GetName() string
}

// Manager manages multiple speech providers with fallback logic
type Manager struct {
	log       *zap.Logger
	providers map[services.SpeechProvider]Provider
	config    *Config
}

// Config holds speech service configuration
type Config struct {
	DefaultProvider     services.SpeechProvider
	EnableFallback      bool
	MaxAudioDuration    time.Duration
	DefaultLanguage     string
	CacheTTL           time.Duration
}

// STTResult represents Speech-to-Text result
type STTResult struct {
	Text              string
	DetectedLanguage  string
	Confidence        float32
	ProcessingTimeMs  int32
}

// TTSResult represents Text-to-Speech result
type TTSResult struct {
	AudioData         []byte
	AudioFormat       string
	DurationSeconds   int32
	ProcessingTimeMs  int32
}

// NewManager creates a new speech provider manager
func NewManager(log *zap.Logger, config *Config) *Manager {
	return &Manager{
		log:       log.Named("speech_manager"),
		providers: make(map[services.SpeechProvider]Provider),
		config:    config,
	}
}

// RegisterProvider registers a speech provider
func (m *Manager) RegisterProvider(providerType services.SpeechProvider, provider Provider) {
	m.log.Info("RegisterProvider called",
		zap.String("provider_type", providerType.String()),
		zap.String("provider_name", provider.GetName()))

	m.providers[providerType] = provider
}

// SpeechToText converts audio to text using specified provider
func (m *Manager) SpeechToText(ctx context.Context, audioData []byte, language string, provider services.SpeechProvider) (*STTResult, error) {
	m.log.Info("SpeechToText called",
		zap.Int("audio_data_size", len(audioData)),
		zap.String("language", language),
		zap.String("provider", provider.String()))

	return nil, status.Errorf(codes.Unimplemented, "SpeechToText not implemented")
}

// TextToSpeech converts text to audio using specified provider
func (m *Manager) TextToSpeech(ctx context.Context, text string, language string, voice string, provider services.SpeechProvider) (*TTSResult, error) {
	m.log.Info("TextToSpeech called",
		zap.String("text_length", func() string {
			if len(text) > 50 {
				return "long"
			} else if len(text) > 0 {
				return "short"
			}
			return "empty"
		}()),
		zap.String("language", language),
		zap.String("voice", voice),
		zap.String("provider", provider.String()))

	return nil, status.Errorf(codes.Unimplemented, "TextToSpeech not implemented")
}

// SelectProvider selects optimal provider based on criteria
func (m *Manager) SelectProvider(userLang string, audioLength time.Duration) services.SpeechProvider {
	m.log.Info("SelectProvider called",
		zap.String("user_lang", userLang),
		zap.Duration("audio_length", audioLength))

	// Return default provider for stub implementation
	return m.config.DefaultProvider
}

// GetAvailableProviders returns list of registered providers
func (m *Manager) GetAvailableProviders() []services.SpeechProvider {
	m.log.Info("GetAvailableProviders called")

	providers := make([]services.SpeechProvider, 0, len(m.providers))
	for providerType := range m.providers {
		providers = append(providers, providerType)
	}

	return providers
}

// ValidateAudio validates audio data format and size
func (m *Manager) ValidateAudio(audioData []byte, format string) error {
	m.log.Debug("ValidateAudio called",
		zap.Int("audio_data_size", len(audioData)),
		zap.String("format", format))

	return status.Errorf(codes.Unimplemented, "ValidateAudio not implemented")
}