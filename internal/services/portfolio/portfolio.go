package portfolio

import "github.com/foxcool/greedy-eye/internal/api/services"

type PortfolioService struct {
	services.UnimplementedPortfolioServiceServer
}

func NewService() *PortfolioService {
	return &PortfolioService{}
}
