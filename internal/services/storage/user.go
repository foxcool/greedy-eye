package storage

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/user"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *StorageService) CreateUser(ctx context.Context, req *services.CreateUserRequest) (*models.User, error) {
	if req.User == nil {
		return nil, status.Errorf(codes.InvalidArgument, "user information is required")
	}

	if req.User.Email == "" || req.User.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "user email and name are required")
	}

	preferences := req.User.Preferences
	if preferences == nil {
		preferences = map[string]string{}
	}

	createdEntUser, err := s.dbClient.User.
		Create().
		SetEmail(req.User.Email).
		SetName(req.User.Name).
		SetPreferences(preferences).
		Save(ctx)
	if err != nil {
		s.log.Error("Failed to create user", zap.Error(err))

		if ent.IsConstraintError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "user creation constraint failed: %v", err)
		}

		return nil, status.Errorf(codes.Internal, "failed to create User: %v", err)
	}

	protoUser, err := entUserToProtoUser(createdEntUser)
	if err != nil {
		s.log.Error("Failed to convert user to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert user to proto: %v", err)
	}

	s.log.Info("User created successfully", zap.String("uuid", createdEntUser.UUID.String()))
	return protoUser, nil
}

func (s *StorageService) GetUser(ctx context.Context, req *services.GetUserRequest) (*models.User, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "user ID is required")
	}

	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user ID format: %v", err)
	}

	entUser, err := s.dbClient.User.
		Query().
		Where(user.UUID(parsedUUID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("User not found", zap.String("uuid", req.Id))
			return nil, status.Errorf(codes.NotFound, "user with ID %s not found", req.Id)
		}

		s.log.Error("Failed to get user", zap.String("uuid", req.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve user: %v", err)
	}

	protoUser, err := entUserToProtoUser(entUser)
	if err != nil {
		s.log.Error("Failed to convert user to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert user to proto: %v", err)
	}

	return protoUser, nil
}

func (s *StorageService) UpdateUser(ctx context.Context, req *services.UpdateUserRequest) (*models.User, error) {
	if req.User == nil || req.User.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "user with ID is required")
	}
	if req.UpdateMask == nil || len(req.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required for update operation")
	}

	parsedUUID, err := stringToUUID(req.User.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user ID format: %v", err)
	}

	entUser, err := s.dbClient.User.Query().Where(user.UUID(parsedUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("User not found", zap.String("uuid", req.User.Id))
			return nil, status.Errorf(codes.NotFound, "user with ID %s not found for update", req.User.Id)
		}
		s.log.Error("Failed to get user", zap.String("uuid", req.User.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve user: %v", err)
	}

	mutation := entUser.Update()
	// Apply changes based on FieldMask
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "email":
			if req.User.Email == "" {
				return nil, status.Errorf(codes.InvalidArgument, "email cannot be empty if included in mask")
			}
			mutation.SetEmail(req.User.Email)
		case "name":
			if req.User.Name == "" {
				return nil, status.Errorf(codes.InvalidArgument, "name cannot be empty if included in mask")
			}
			mutation.SetName(req.User.Name)
		case "preferences":
			// If preferences is nil but included in mask, set to empty map
			if req.User.Preferences == nil {
				mutation.SetPreferences(map[string]string{})
			} else {
				mutation.SetPreferences(req.User.Preferences)
			}
		default:
			s.log.Warn("UpdateUser requested with unknown field in mask", zap.String("path", path))
		}
	}
	if _, err := mutation.Save(ctx); err != nil {
		s.log.Error("Failed to update user", zap.String("uuid", req.User.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update user: %v", err)
	}
	entUser, err = s.dbClient.User.Query().Where(user.UUID(parsedUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("User not found", zap.String("uuid", req.User.Id))
			return nil, status.Errorf(codes.NotFound, "user with ID %s not found after update", req.User.Id)
		}
		s.log.Error("Failed to get user after update", zap.String("uuid", req.User.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve user: %v", err)
	}
	protoUser, err := entUserToProtoUser(entUser)
	if err != nil {
		s.log.Error("Failed to convert user to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert user to proto: %v", err)
	}
	return protoUser, nil
}

func (s *StorageService) DeleteUser(ctx context.Context, req *services.DeleteUserRequest) (*emptypb.Empty, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "user ID is required")
	}

	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user ID format: %v", err)
	}

	deletedCount, err := s.dbClient.User.
		Delete().
		Where(user.UUID(parsedUUID)).
		Exec(ctx)

	if err != nil {
		// Handle constraint error
		if ent.IsConstraintError(err) {
			s.log.Error("Failed to delete user due to constraint", zap.String("uuid", req.Id), zap.Error(err))
			return nil, status.Errorf(codes.FailedPrecondition, "cannot delete user due to existing dependencies: %v", err)
		}
		s.log.Error("Failed to delete user", zap.String("uuid", req.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to delete user: %v", err)
	}

	if deletedCount == 0 {
		s.log.Warn("Attempted to delete non-existent user", zap.String("uuid", req.Id))
		return nil, status.Errorf(codes.NotFound, "user with ID %s not found", req.Id)
	}

	s.log.Info("User deleted successfully", zap.String("uuid", req.Id))
	return &emptypb.Empty{}, nil
}
