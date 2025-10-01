package messenger

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/services"
)

// Service implements the TelegramBotService gRPC interface
type Service struct {
	log *zap.Logger
}

// NewService creates a new TelegramBotService instance
func NewService(log *zap.Logger) *Service {
	return &Service{
		log: log.Named("telegram"),
	}
}

// ProcessTelegramUpdate processes incoming Telegram webhook updates
func (s *Service) ProcessTelegramUpdate(ctx context.Context, req *services.ProcessTelegramUpdateRequest) (*services.ProcessTelegramUpdateResponse, error) {
	s.log.Info("ProcessTelegramUpdate called",
		zap.String("webhook_secret_length", func() string {
			if len(req.WebhookSecret) > 0 {
				return "present"
			}
			return "empty"
		}()),
		zap.Int("update_json_length", len(req.UpdateJson)))

	return nil, status.Errorf(codes.Unimplemented, "ProcessTelegramUpdate not implemented")
}

// SendNotification sends notification to specific user
func (s *Service) SendNotification(ctx context.Context, req *services.SendNotificationRequest) (*services.SendNotificationResponse, error) {
	s.log.Info("SendNotification called",
		zap.String("telegram_id", req.Notification.TelegramId),
		zap.String("notification_type", req.Notification.Type.String()),
		zap.Bool("force_send", req.ForceSend))

	return nil, status.Errorf(codes.Unimplemented, "SendNotification not implemented")
}

// SendBulkNotifications sends bulk notifications to multiple users
func (s *Service) SendBulkNotifications(ctx context.Context, req *services.SendBulkNotificationsRequest) (*services.SendBulkNotificationsResponse, error) {
	s.log.Info("SendBulkNotifications called",
		zap.Int("notifications_count", len(req.Notifications)),
		zap.Int32("batch_size", req.BatchSize),
		zap.Int32("delay_ms", req.DelayMs))

	return nil, status.Errorf(codes.Unimplemented, "SendBulkNotifications not implemented")
}

// ManageAlerts manages user alert subscriptions
func (s *Service) ManageAlerts(ctx context.Context, req *services.ManageAlertsRequest) (*services.ManageAlertsResponse, error) {
	s.log.Info("ManageAlerts called",
		zap.String("telegram_id", req.TelegramId),
		zap.String("operation", req.Operation.String()),
		zap.String("alert_id", req.AlertId))

	return nil, status.Errorf(codes.Unimplemented, "ManageAlerts not implemented")
}

// GetUserSession gets user session information
func (s *Service) GetUserSession(ctx context.Context, req *services.GetUserSessionRequest) (*services.GetUserSessionResponse, error) {
	s.log.Info("GetUserSession called",
		zap.String("telegram_id", req.TelegramId))

	return nil, status.Errorf(codes.Unimplemented, "GetUserSession not implemented")
}

// UpdateUserSession updates user session context
func (s *Service) UpdateUserSession(ctx context.Context, req *services.UpdateUserSessionRequest) (*services.UpdateUserSessionResponse, error) {
	s.log.Info("UpdateUserSession called",
		zap.String("telegram_id", req.Session.TelegramId),
		zap.String("current_operation", req.Session.CurrentOperation),
		zap.String("state", req.Session.State))

	return nil, status.Errorf(codes.Unimplemented, "UpdateUserSession not implemented")
}

// RegisterTelegramUser registers new Telegram user or updates existing
func (s *Service) RegisterTelegramUser(ctx context.Context, req *services.RegisterTelegramUserRequest) (*services.RegisterTelegramUserResponse, error) {
	s.log.Info("RegisterTelegramUser called",
		zap.String("telegram_id", req.TelegramUser.TelegramId),
		zap.String("username", req.TelegramUser.Username),
		zap.Bool("link_existing_user", req.LinkExistingUser),
		zap.String("existing_user_id", req.ExistingUserId))

	return nil, status.Errorf(codes.Unimplemented, "RegisterTelegramUser not implemented")
}

// GetTelegramUser gets Telegram user information
func (s *Service) GetTelegramUser(ctx context.Context, req *services.GetTelegramUserRequest) (*services.GetTelegramUserResponse, error) {
	s.log.Info("GetTelegramUser called",
		zap.String("telegram_id", req.TelegramId))

	return nil, status.Errorf(codes.Unimplemented, "GetTelegramUser not implemented")
}

// ProcessVoiceMessage processes voice message (Speech-to-Text)
func (s *Service) ProcessVoiceMessage(ctx context.Context, req *services.ProcessVoiceMessageRequest) (*services.ProcessVoiceMessageResponse, error) {
	s.log.Info("ProcessVoiceMessage called",
		zap.String("telegram_id", req.TelegramId),
		zap.String("audio_format", req.AudioFormat),
		zap.Int32("duration_seconds", req.DurationSeconds),
		zap.String("language_hint", req.LanguageHint),
		zap.String("provider", req.Provider.String()))

	return nil, status.Errorf(codes.Unimplemented, "ProcessVoiceMessage not implemented")
}

// ConvertTextToSpeech converts text to speech
func (s *Service) ConvertTextToSpeech(ctx context.Context, req *services.ConvertTextToSpeechRequest) (*services.ConvertTextToSpeechResponse, error) {
	s.log.Info("ConvertTextToSpeech called",
		zap.String("text_length", func() string {
			if len(req.Text) > 50 {
				return "long"
			} else if len(req.Text) > 0 {
				return "short"
			}
			return "empty"
		}()),
		zap.String("language", req.Language),
		zap.String("voice", req.Voice),
		zap.String("provider", req.Provider.String()))

	return nil, status.Errorf(codes.Unimplemented, "ConvertTextToSpeech not implemented")
}

// GetBotStats gets bot statistics and health
func (s *Service) GetBotStats(ctx context.Context, req *services.GetBotStatsRequest) (*services.GetBotStatsResponse, error) {
	s.log.Info("GetBotStats called")

	return nil, status.Errorf(codes.Unimplemented, "GetBotStats not implemented")
}