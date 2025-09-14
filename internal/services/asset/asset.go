package asset

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service implements the AssetService gRPC service.
type Service struct {
	log *zap.Logger
}

// NewService creates a new AssetService.
func NewService(logger *zap.Logger) *Service {
	return &Service{
		log: logger,
	}
}

// EnrichAssetData enriches asset data from external sources
func (s *Service) EnrichAssetData(ctx context.Context, req *services.EnrichAssetDataRequest) (*models.Asset, error) {
	s.log.Info("EnrichAssetData called", zap.String("asset_id", req.AssetId))
	return nil, status.Errorf(codes.Unimplemented, "EnrichAssetData not implemented")
}

// FindSimilarAssets finds assets similar to the given one
func (s *Service) FindSimilarAssets(ctx context.Context, req *services.FindSimilarAssetsRequest) (*services.ListAssetsResponse, error) {
	s.log.Info("FindSimilarAssets called", zap.String("asset_id", req.AssetId))
	return nil, status.Errorf(codes.Unimplemented, "FindSimilarAssets not implemented")
}
