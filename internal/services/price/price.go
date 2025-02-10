package price

import (
	grpc "google.golang.org/grpc"
)

type PricingService struct {
	UnimplementedPricingServiceServer
	PriceStorage PriceStorage
}

type PriceStorage interface {
}

func NewPricingService(priceStorage PriceStorage) *PricingService {
	return &PricingService{}
}

func (s *PricingService) Register(server *grpc.Server) {
	RegisterPricingServiceServer(server, s)
}
