package portfolio

import grpc "google.golang.org/grpc"

type PortfolioService struct {
	UnimplementedPortfolioServiceServer
}

func NewPortfolioService() *PortfolioService {
	return &PortfolioService{}
}

func (s *PortfolioService) Register(server *grpc.Server) {
	RegisterPortfolioServiceServer(server, s)
}
