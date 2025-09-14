package user

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestUserService_UpdateUserPreferences(t *testing.T) {
	logger := zap.NewNop()
	service := NewService(logger)
	
	t.Run("should return unimplemented", func(t *testing.T) {
		req := &services.UpdateUserPreferencesRequest{
			UserId: "test-user-id",
			PreferencesToUpdate: map[string]string{
				"theme":    "dark",
				"language": "en",
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"theme", "language"},
			},
		}
		
		resp, err := service.UpdateUserPreferences(context.Background(), req)
		
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
		assert.Contains(t, err.Error(), "UpdateUserPreferences not implemented")
	})
}