package price

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PriceService struct {
	log *zap.Logger
}

func NewService(logger *zap.Logger) *PriceService {
	return &PriceService{
		log: logger,
	}
}

// FetchExternalPrices triggers fetching of latest prices from external sources
func (s *PriceService) FetchExternalPrices(ctx context.Context, req *services.FetchExternalPricesRequest) (*services.FetchExternalPricesResponse, error) {
	s.log.Info("FetchExternalPrices called", zap.Strings("source_ids", req.SourceIds), zap.Strings("asset_ids", req.AssetIds))
	return nil, status.Errorf(codes.Unimplemented, "FetchExternalPrices not implemented")
}
