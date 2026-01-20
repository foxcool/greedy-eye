package portfolio

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPortfolioService_CalculatePortfolioValue(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(logger)
	
	t.Run("should return unimplemented", func(t *testing.T) {
		req := &services.CalculatePortfolioValueRequest{
			PortfolioId:   "test-portfolio-id",
			QuoteAssetId:  "USD",
			AtTime:        timestamppb.Now(),
		}
		
		resp, err := service.CalculatePortfolioValue(context.Background(), req)
		
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
		assert.Contains(t, err.Error(), "CalculatePortfolioValue not implemented")
	})
}

func TestPortfolioService_GetPortfolioPerformance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(logger)
	
	t.Run("should return unimplemented", func(t *testing.T) {
		req := &services.GetPortfolioPerformanceRequest{
			PortfolioId:      "test-portfolio-id",
			From:             timestamppb.Now(),
			To:               timestamppb.Now(),
			BenchmarkAssetId: "BTC",
		}
		
		resp, err := service.GetPortfolioPerformance(context.Background(), req)
		
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
		assert.Contains(t, err.Error(), "GetPortfolioPerformance not implemented")
	})
}