package speech

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// YandexProvider implements Yandex SpeechKit API
type YandexProvider struct {
	log      *zap.Logger
	apiKey   string
	folderID string
}

// NewYandexProvider creates a new Yandex speech provider
func NewYandexProvider(log *zap.Logger, apiKey, folderID string) *YandexProvider {
	return &YandexProvider{
		log:      log.Named("yandex_speech"),
		apiKey:   apiKey,
		folderID: folderID,
	}
}

// SpeechToText converts audio to text using Yandex SpeechKit
func (p *YandexProvider) SpeechToText(ctx context.Context, audioData []byte, language string) (*STTResult, error) {
	p.log.Info("SpeechToText called",
		zap.Int("audio_data_size", len(audioData)),
		zap.String("language", language))

	return nil, status.Errorf(codes.Unimplemented, "YandexProvider SpeechToText not implemented")
}

// TextToSpeech converts text to speech using Yandex SpeechKit
func (p *YandexProvider) TextToSpeech(ctx context.Context, text string, language string, voice string) (*TTSResult, error) {
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

	return nil, status.Errorf(codes.Unimplemented, "YandexProvider TextToSpeech not implemented")
}

// GetSupportedLanguages returns supported languages
func (p *YandexProvider) GetSupportedLanguages() []string {
	return []string{"ru-RU", "en-US", "tr-TR", "kk-KK", "uz-UZ", "he-IL"}
}

// GetName returns provider name
func (p *YandexProvider) GetName() string {
	return "Yandex SpeechKit"
}