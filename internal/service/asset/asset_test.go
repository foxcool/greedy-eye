//go:build ignore
package asset

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAssetService_EnrichAssetData(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Create a mock storage client - using nil for tests that don't need it
	mockStorage := storage.NewLocalClient(nil)
	service := NewService(logger, mockStorage)

	t.Run("should return NotFound for non-existent asset", func(t *testing.T) {
		req := &services.EnrichAssetDataRequest{
			AssetId: "non-existent-asset",
			Sources: []string{"test-source"},
		}

		resp, err := service.EnrichAssetData(context.Background(), req)

		assert.Nil(t, resp)
		assert.Error(t, err)
		// Service calls GetAsset first, which returns NotFound
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestAssetService_FindSimilarAssets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockStorage := storage.NewLocalClient(nil)
	service := NewService(logger, mockStorage)

	t.Run("should return NotFound for non-existent asset", func(t *testing.T) {
		req := &services.FindSimilarAssetsRequest{
			AssetId: "test-asset-id",
			Limit:   10,
		}

		resp, err := service.FindSimilarAssets(context.Background(), req)

		assert.Nil(t, resp)
		assert.Error(t, err)
		// Method is implemented, but GetAsset returns NotFound
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}