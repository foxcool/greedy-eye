//go:build ignore
package user

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UserService struct {
	log           *slog.Logger
	storageClient services.StorageServiceClient
}

func NewService(logger *slog.Logger, storageClient services.StorageServiceClient) *UserService {
	return &UserService{
		log:           logger,
		storageClient: storageClient,
	}
}

// UpdateUserPreferences updates user-specific preferences
func (s *UserService) UpdateUserPreferences(ctx context.Context, req *services.UpdateUserPreferencesRequest) (*models.User, error) {
	s.log.Info("UpdateUserPreferences called",
		slog.String("user_id", req.UserId),
		slog.Any("preferences", req.PreferencesToUpdate))

	// Validate user exists
	user, err := s.storageClient.GetUser(ctx, &services.GetUserRequest{Id: req.UserId})
	if err != nil {
		s.log.Error("Failed to get user", slog.String("user_id", req.UserId), slog.Any("error",err))
		return nil, status.Errorf(codes.NotFound, "User not found: %v", err)
	}

	// Validate preference keys
	validPreferenceKeys := map[string]bool{
		"default_currency":           true,
		"notification_channels":      true,
		"price_alert_frequency":      true,
		"portfolio_rebalance_mode":   true,
		"risk_tolerance":             true,
		"preferred_exchanges":        true,
		"data_providers":             true,
		"telegram_notifications":     true,
		"email_notifications":        true,
		"timezone":                   true,
	}

	for key := range req.PreferencesToUpdate {
		if !validPreferenceKeys[key] {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid preference key: %s", key)
		}
	}

	// Business logic for specific preferences
	if currency, exists := req.PreferencesToUpdate["default_currency"]; exists {
		if !isValidCurrency(currency) {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid currency: %s", currency)
		}
	}

	if riskTolerance, exists := req.PreferencesToUpdate["risk_tolerance"]; exists {
		if !isValidRiskTolerance(riskTolerance) {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid risk tolerance: %s", riskTolerance)
		}
	}

	// Merge preferences with existing data
	updatedPreferences := make(map[string]string)
	if user.Preferences != nil {
		for k, v := range user.Preferences {
			updatedPreferences[k] = v
		}
	}
	for k, v := range req.PreferencesToUpdate {
		updatedPreferences[k] = v
	}

	// Update user via StorageService
	updateReq := &services.UpdateUserRequest{
		User: &models.User{
			Id:          user.Id,
			Email:       user.Email,
			Name:        user.Name,
			Preferences: updatedPreferences,
			CreatedAt:   user.CreatedAt,
		},
	}

	updatedUser, err := s.storageClient.UpdateUser(ctx, updateReq)
	if err != nil {
		s.log.Error("Failed to update user", slog.String("user_id", req.UserId), slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "Failed to update user preferences: %v", err)
	}

	s.log.Info("User preferences updated successfully", slog.String("user_id", req.UserId))
	return updatedUser, nil
}

// GetUserNotificationChannels returns configured notification channels for a user
func (s *UserService) GetUserNotificationChannels(ctx context.Context, userID string) ([]string, error) {
	user, err := s.storageClient.GetUser(ctx, &services.GetUserRequest{Id: userID})
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	var channels []string
	if user.Preferences != nil {
		if user.Preferences["telegram_notifications"] == "true" {
			channels = append(channels, "telegram")
		}
		if user.Preferences["email_notifications"] == "true" {
			channels = append(channels, "email")
		}
	}

	return channels, nil
}

// Helper functions for validation
func isValidCurrency(currency string) bool {
	validCurrencies := map[string]bool{
		"USD": true, "EUR": true, "RUB": true, "BTC": true, "ETH": true,
	}
	return validCurrencies[currency]
}

func isValidRiskTolerance(tolerance string) bool {
	validTolerances := map[string]bool{
		"conservative": true, "moderate": true, "aggressive": true,
	}
	return validTolerances[tolerance]
}
