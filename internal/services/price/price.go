package price

import (
	"github.com/foxcool/greedy-eye/internal/api/services"
)

type PricingService struct {
	services.UnimplementedPriceServiceServer
}

func NewService() *PricingService {
	return &PricingService{}
}
