package speech

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OpenAIProvider implements OpenAI Whisper and TTS API
type OpenAIProvider struct {
	log    *zap.Logger
	apiKey string
}

// NewOpenAIProvider creates a new OpenAI speech provider
func NewOpenAIProvider(log *zap.Logger, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		log:    log.Named("openai_speech"),
		apiKey: apiKey,
	}
}

// SpeechToText converts audio to text using OpenAI Whisper
func (p *OpenAIProvider) SpeechToText(ctx context.Context, audioData []byte, language string) (*STTResult, error) {
	p.log.Info("SpeechToText called",
		zap.Int("audio_data_size", len(audioData)),
		zap.String("language", language))

	return nil, status.Errorf(codes.Unimplemented, "OpenAIProvider SpeechToText not implemented")
}

// TextToSpeech converts text to speech using OpenAI TTS
func (p *OpenAIProvider) TextToSpeech(ctx context.Context, text string, language string, voice string) (*TTSResult, error) {
	p.log.Info("TextToSpeech called",
		zap.String("text_length", func() string {
			if len(text) > 50 {
				return "long"
			} else if len(text) > 0 {
				return "short"
			}
			return "empty"
		}()),
		zap.String("language", language),
		zap.String("voice", voice))

	return nil, status.Errorf(codes.Unimplemented, "OpenAIProvider TextToSpeech not implemented")
}

// GetSupportedLanguages returns supported languages (Whisper supports 100+ languages)
func (p *OpenAIProvider) GetSupportedLanguages() []string {
	return []string{
		"af", "ar", "hy", "az", "be", "bs", "bg", "ca", "zh", "hr", "cs", "da", "nl", "en", "et", "fi", "fr",
		"gl", "de", "el", "he", "hi", "hu", "is", "id", "it", "ja", "kn", "kk", "ko", "lv", "lt", "mk", "ms",
		"mr", "mi", "ne", "no", "fa", "pl", "pt", "ro", "ru", "sr", "sk", "sl", "es", "sw", "sv", "tl", "ta",
		"th", "tr", "uk", "ur", "vi", "cy", "yi",
	}
}

// GetName returns provider name
func (p *OpenAIProvider) GetName() string {
	return "OpenAI Whisper"
}