package portfolio

import (
	"context"
	"log/slog"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PortfolioService struct {
	log *slog.Logger
}

func NewService(logger *slog.Logger) *PortfolioService {
	return &PortfolioService{
		log: logger,
	}
}

// CalculatePortfolioValue calculates the current total value of a portfolio
func (s *PortfolioService) CalculatePortfolioValue(ctx context.Context, req *services.CalculatePortfolioValueRequest) (*services.PortfolioValueResponse, error) {
	s.log.Info("CalculatePortfolioValue called", slog.String("portfolio_id", req.PortfolioId))
	return nil, status.Errorf(codes.Unimplemented, "CalculatePortfolioValue not implemented")
}

// GetPortfolioPerformance retrieves performance metrics for a portfolio
func (s *PortfolioService) GetPortfolioPerformance(ctx context.Context, req *services.GetPortfolioPerformanceRequest) (*services.PortfolioPerformanceResponse, error) {
	s.log.Info("GetPortfolioPerformance called", slog.String("portfolio_id", req.PortfolioId))
	return nil, status.Errorf(codes.Unimplemented, "GetPortfolioPerformance not implemented")
}
