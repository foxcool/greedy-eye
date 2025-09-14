package user

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UserService struct {
	log *zap.Logger
}

func NewService(logger *zap.Logger) *UserService {
	return &UserService{
		log: logger,
	}
}

// UpdateUserPreferences updates user-specific preferences
func (s *UserService) UpdateUserPreferences(ctx context.Context, req *services.UpdateUserPreferencesRequest) (*models.User, error) {
	s.log.Info("UpdateUserPreferences called", zap.String("user_id", req.UserId))
	return nil, status.Errorf(codes.Unimplemented, "UpdateUserPreferences not implemented")
}
