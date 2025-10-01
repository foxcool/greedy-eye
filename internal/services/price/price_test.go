package price

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestPriceService_FetchExternalPrices(t *testing.T) {
	logger := zap.NewNop()
	var mockStorage services.StorageServiceClient = storage.NewLocalClient(nil)
	var mockAsset services.AssetServiceClient = nil  // Not needed for this test
	service := NewService(logger, mockStorage, mockAsset)

	t.Run("should return response with errors for unsupported sources", func(t *testing.T) {
		req := &services.FetchExternalPricesRequest{
			SourceIds: []string{"coingecko", "binance"},
			AssetIds:  []string{"bitcoin", "ethereum"},
		}

		resp, err := service.FetchExternalPrices(context.Background(), req)

		// Method is implemented and returns response (not gRPC error)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		// Binance is not implemented, so should have errors
		assert.NotEmpty(t, resp.Errors)
	})
}