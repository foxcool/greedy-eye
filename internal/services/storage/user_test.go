package storage

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func createTestUser(t *testing.T, s *StorageService, name, email string) *models.User {
	user, err := s.CreateUser(t.Context(), &services.CreateUserRequest{
		User: &models.User{Name: name, Email: email},
	})
	if !assert.NoError(t, err) || !assert.NotNil(t, user) {
		assert.FailNow(t, "can't create user")
	}

	return user
}

func TestCreateUser(t *testing.T) {
	storageService := getTransactionedService(t)

	t.Run("Create regular user", func(t *testing.T) {
		req := &services.CreateUserRequest{
			User: &models.User{
				Email: "test1@example.com",
				Name:  "Test User 1",
			},
		}
		user, err := storageService.CreateUser(t.Context(), req)
		if assert.NoError(t, err, "user creation failed") {
			assert.NotNil(t, user)
			assert.Equal(t, req.User.Email, user.Email)
			assert.Equal(t, req.User.Name, user.Name)
		}
	})

	t.Run("Missing required fields", func(t *testing.T) {
		req := &services.CreateUserRequest{
			User: &models.User{
				Name: "Test User 2",
			},
		}
		res, err := storageService.CreateUser(t.Context(), req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Duplicate email", func(t *testing.T) {
		req := &services.CreateUserRequest{
			User: &models.User{
				Email: "test1@example.com", // Duplicate email
				Name:  "Test User 3",
			},
		}
		res, err := storageService.CreateUser(t.Context(), req)
		assert.Equal(t, codes.AlreadyExists, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Nil user in request", func(t *testing.T) {
		req := &services.CreateUserRequest{User: nil}
		res, err := storageService.CreateUser(t.Context(), req)
		assert.Error(t, err, "user creation with nil User should fail")
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})
}

func TestGetUser(t *testing.T) {
	storageService := getTransactionedService(t)
	createdUser := createTestUser(t, storageService, "test", "test@user.email")

	t.Run("Get existing user by ID", func(t *testing.T) {
		req1 := &services.GetUserRequest{Id: createdUser.Id}
		user, err := storageService.GetUser(t.Context(), req1)
		if assert.NoError(t, err) {
			assert.Equal(t, createdUser.Id, user.Id)
			assert.Equal(t, createdUser.Email, user.Email)
			assert.Equal(t, createdUser.Name, user.Name)
		}
	})

	t.Run("Get  non-existent user by ID", func(t *testing.T) {
		nonExistentID := uuid.New().String()
		req := &services.GetUserRequest{Id: nonExistentID}
		res, err := storageService.GetUser(t.Context(), req)
		assert.Error(t, err, "expected error for non-existent user")
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Invalid ID format", func(t *testing.T) {
		invalidID := "not-a-valid-uuid"
		req := &services.GetUserRequest{Id: invalidID}
		res, err := storageService.GetUser(t.Context(), req)
		assert.Error(t, err, "expected error for invalid ID format")
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Empty ID in request", func(t *testing.T) {
		req := &services.GetUserRequest{Id: ""}
		res, err := storageService.GetUser(t.Context(), req)
		assert.Error(t, err, "expected error for invalid ID format")
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})
}

func TestUpdateUser(t *testing.T) {
	storageService := getTransactionedService(t)
	createdUser := createTestUser(t, storageService, "User", "test@user.com")
	createdUser2 := createTestUser(t, storageService, "User2", "test2@user.com")

	t.Run("Update user name and email", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User: &models.User{
				Id:    createdUser.Id,
				Name:  "Updated User Name",
				Email: "updateduser@example.com",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "email"}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)
		if assert.NoError(t, err) {
			assert.Equal(t, req.User.Name, res.Name)
			assert.Equal(t, req.User.Email, res.Email)
		}
	})

	t.Run("Partial update (only email)", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User: &models.User{
				Id:    createdUser.Id,
				Email: "updateduser2@example.com",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"email"}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)
		if assert.NoError(t, err) {
			assert.Equal(t, req.User.Email, res.Email)
			assert.Equal(t, res.Name, res.Name)
		}
	})

	t.Run("Update with duplicate email", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User: &models.User{
				Id:    createdUser.Id,
				Email: createdUser2.Email,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"email"}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)

		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("Update with non-existent ID", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User: &models.User{
				Id:   uuid.New().String(), // Non-existent ID
				Name: "Should Not Exist",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Missing user ID in request", func(t *testing.T) {
		updateReq5 := &services.UpdateUserRequest{
			User:       &models.User{Name: "No ID"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdateUser(t.Context(), updateReq5)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Invalid user ID format", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User:       &models.User{Id: "invalid-id-format", Name: "Invalid ID"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Empty update mask", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User:       &models.User{Id: createdUser.Id, Name: "New Name"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Missing User in Request", func(t *testing.T) {
		req := &services.UpdateUserRequest{
			User:       nil,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdateUser(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})
}

func TestDeleteUser(t *testing.T) {
	storageService := getTransactionedService(t)
	createdUser := createTestUser(t, storageService, "User", "test@user.com")

	t.Run("Delete existing user", func(t *testing.T) {
		_, err := storageService.DeleteUser(t.Context(), &services.DeleteUserRequest{Id: createdUser.Id})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "can't delete user")
		}

		// Verify deletion
		req := &services.GetUserRequest{Id: createdUser.Id}
		res, err := storageService.GetUser(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Delete non-existent user", func(t *testing.T) {
		req := &services.DeleteUserRequest{Id: uuid.New().String()} // Non-existent UUID
		_, err := storageService.DeleteUser(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Invalid ID format", func(t *testing.T) {
		deleteReq3 := &services.DeleteUserRequest{Id: "invalid-deletion-id"}
		_, err3 := storageService.DeleteUser(t.Context(), deleteReq3)
		if assert.Error(t, err3) {
			assert.Equal(t, codes.InvalidArgument, status.Code(err3))
		}
	})

	t.Run("Empty ID in request", func(t *testing.T) {
		deleteReq4 := &services.DeleteUserRequest{Id: ""}
		_, err4 := storageService.DeleteUser(t.Context(), deleteReq4)
		if assert.Error(t, err4) {
			assert.Equal(t, codes.InvalidArgument, status.Code(err4))
		}
	})
}
