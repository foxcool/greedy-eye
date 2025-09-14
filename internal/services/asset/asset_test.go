package asset

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAssetService_EnrichAssetData(t *testing.T) {
	logger := zap.NewNop()
	service := NewService(logger)
	
	t.Run("should return unimplemented", func(t *testing.T) {
		req := &services.EnrichAssetDataRequest{
			AssetId: "test-asset-id",
			Sources: []string{"test-source"},
		}
		
		resp, err := service.EnrichAssetData(context.Background(), req)
		
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
		assert.Contains(t, err.Error(), "EnrichAssetData not implemented")
	})
}

func TestAssetService_FindSimilarAssets(t *testing.T) {
	logger := zap.NewNop()
	service := NewService(logger)
	
	t.Run("should return unimplemented", func(t *testing.T) {
		req := &services.FindSimilarAssetsRequest{
			AssetId: "test-asset-id",
			Limit:   10,
		}
		
		resp, err := service.FindSimilarAssets(context.Background(), req)
		
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
		assert.Contains(t, err.Error(), "FindSimilarAssets not implemented")
	})
}