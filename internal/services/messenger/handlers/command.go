package handlers

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/models"
)

// CommandHandler handles inline commands processing
type CommandHandler struct {
	log *zap.Logger
}

// NewCommandHandler creates a new CommandHandler instance
func NewCommandHandler(log *zap.Logger) *CommandHandler {
	return &CommandHandler{
		log: log.Named("command_handler"),
	}
}

// CommandRequest represents a command execution request
type CommandRequest struct {
	TelegramID string
	Command    string
	Args       []string
	Context    *models.SessionContext
}

// CommandResponse represents a command execution response
type CommandResponse struct {
	Text         string
	Format       models.ResponseFormat
	Buttons      []*models.InlineButton
	EnableVoice  bool
	UpdateSession bool
	NewContext   *models.SessionContext
}

// HandleCommand processes inline commands
func (h *CommandHandler) HandleCommand(ctx context.Context, req *CommandRequest) (*CommandResponse, error) {
	h.log.Info("HandleCommand called",
		zap.String("telegram_id", req.TelegramID),
		zap.String("command", req.Command),
		zap.Strings("args", req.Args))

	return nil, status.Errorf(codes.Unimplemented, "HandleCommand not implemented")
}

// ValidateCommand validates command permissions and rate limits
func (h *CommandHandler) ValidateCommand(ctx context.Context, telegramID string, command string) error {
	h.log.Info("ValidateCommand called",
		zap.String("telegram_id", telegramID),
		zap.String("command", command))

	return status.Errorf(codes.Unimplemented, "ValidateCommand not implemented")
}

// GetAvailableCommands returns list of available commands for user
func (h *CommandHandler) GetAvailableCommands(ctx context.Context, telegramID string) ([]string, error) {
	h.log.Info("GetAvailableCommands called",
		zap.String("telegram_id", telegramID))

	return nil, status.Errorf(codes.Unimplemented, "GetAvailableCommands not implemented")
}

// FormatCommandHelp formats help text for commands
func (h *CommandHandler) FormatCommandHelp(command string) (string, error) {
	h.log.Info("FormatCommandHelp called",
		zap.String("command", command))

	return "", status.Errorf(codes.Unimplemented, "FormatCommandHelp not implemented")
}