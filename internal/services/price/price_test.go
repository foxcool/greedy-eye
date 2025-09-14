package price

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPriceService_FetchExternalPrices(t *testing.T) {
	logger := zap.NewNop()
	service := NewService(logger)
	
	t.Run("should return unimplemented", func(t *testing.T) {
		req := &services.FetchExternalPricesRequest{
			SourceIds: []string{"coingecko", "binance"},
			AssetIds:  []string{"bitcoin", "ethereum"},
		}
		
		resp, err := service.FetchExternalPrices(context.Background(), req)
		
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
		assert.Contains(t, err.Error(), "FetchExternalPrices not implemented")
	})
}