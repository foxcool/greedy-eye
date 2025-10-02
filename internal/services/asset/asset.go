package asset

import (
	"context"
	"strings"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service implements the AssetService gRPC service.
type Service struct {
	log           *zap.Logger
	storageClient services.StorageServiceClient
}

// NewService creates a new AssetService.
func NewService(logger *zap.Logger, storageClient services.StorageServiceClient) *Service {
	return &Service{
		log:           logger,
		storageClient: storageClient,
	}
}

// EnrichAssetData enriches asset data from external sources
func (s *Service) EnrichAssetData(ctx context.Context, req *services.EnrichAssetDataRequest) (*models.Asset, error) {
	s.log.Info("EnrichAssetData called", zap.String("asset_id", req.AssetId))

	// Get existing asset data
	asset, err := s.storageClient.GetAsset(ctx, &services.GetAssetRequest{Id: req.AssetId})
	if err != nil {
		s.log.Error("Failed to get asset", zap.String("asset_id", req.AssetId), zap.Error(err))
		return nil, status.Errorf(codes.NotFound, "Asset not found: %v", err)
	}

	// TODO: Enrich from CoinGecko if it's a cryptocurrency
	// Enrichment logic will be implemented using the coingecko adapter
	// Currently returning original asset without enrichment

	// For non-crypto assets, return as-is for now
	s.log.Info("Asset enrichment skipped for non-crypto asset",
		zap.String("asset_id", req.AssetId),
		zap.String("type", asset.Type.String()))
	return asset, nil
}

// FindSimilarAssets finds assets similar to the given one
func (s *Service) FindSimilarAssets(ctx context.Context, req *services.FindSimilarAssetsRequest) (*services.ListAssetsResponse, error) {
	s.log.Info("FindSimilarAssets called", zap.String("asset_id", req.AssetId))

	// Get the source asset
	asset, err := s.storageClient.GetAsset(ctx, &services.GetAssetRequest{Id: req.AssetId})
	if err != nil {
		s.log.Error("Failed to get asset", zap.String("asset_id", req.AssetId), zap.Error(err))
		return nil, status.Errorf(codes.NotFound, "Asset not found: %v", err)
	}

	// Find similar assets based on different criteria
	var similarAssets []*models.Asset

	// 1. Find assets with same type
	listReq := &services.ListAssetsRequest{
		PageSize: func() *int32 { i := int32(20); return &i }(),
	}

	response, err := s.storageClient.ListAssets(ctx, listReq)
	if err != nil {
		s.log.Error("Failed to list assets by type", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "Failed to find similar assets: %v", err)
	}

	// Filter out the original asset and find truly similar ones
	for _, candidate := range response.Assets {
		if candidate.Id == asset.Id {
			continue // Skip the original asset
		}

		// Filter by type first, then check similarity
		if candidate.Type == asset.Type && s.areAssetsSimilar(asset, candidate) {
			similarAssets = append(similarAssets, candidate)
		}
	}

	// Limit results
	if len(similarAssets) > 10 {
		similarAssets = similarAssets[:10]
	}

	s.log.Info("Found similar assets",
		zap.String("asset_id", req.AssetId),
		zap.Int("count", len(similarAssets)))

	return &services.ListAssetsResponse{
		Assets: similarAssets,
	}, nil
}

// areAssetsSimilar determines if two assets are similar based on various criteria
func (s *Service) areAssetsSimilar(asset1, asset2 *models.Asset) bool {
	// Same type is a basic requirement
	if asset1.Type != asset2.Type {
		return false
	}

	// Check symbol similarity (for crypto assets)
	if asset1.Type == models.AssetType_ASSET_TYPE_CRYPTOCURRENCY {
		return s.areCryptoAssetsSimilar(asset1, asset2)
	}

	// Check name similarity
	return s.areNamesSimilar(asset1.Name, asset2.Name)
}

// areCryptoAssetsSimilar checks similarity for cryptocurrency assets
func (s *Service) areCryptoAssetsSimilar(asset1, asset2 *models.Asset) bool {
	// Check if symbols are related (e.g., BTC/WBTC, ETH/WETH)
	if asset1.Symbol == nil || asset2.Symbol == nil {
		return false
	}
	symbol1 := strings.ToUpper(*asset1.Symbol)
	symbol2 := strings.ToUpper(*asset2.Symbol)

	// Direct symbol similarity
	if strings.Contains(symbol1, symbol2) || strings.Contains(symbol2, symbol1) {
		return true
	}

	// Check for wrapped tokens
	if (strings.HasPrefix(symbol1, "W") && symbol1[1:] == symbol2) ||
		(strings.HasPrefix(symbol2, "W") && symbol2[1:] == symbol1) {
		return true
	}

	// Check tags for similar blockchain or protocol
	asset1Tags := make(map[string]bool)
	for _, tag := range asset1.Tags {
		asset1Tags[tag] = true
	}

	for _, tag := range asset2.Tags {
		if asset1Tags[tag] && strings.Contains(tag, "blockchain:") {
			return true
		}
	}

	return false
}

// areNamesSimilar checks if asset names are similar
func (s *Service) areNamesSimilar(name1, name2 string) bool {
	name1 = strings.ToLower(strings.TrimSpace(name1))
	name2 = strings.ToLower(strings.TrimSpace(name2))

	// Check if one name contains the other
	if strings.Contains(name1, name2) || strings.Contains(name2, name1) {
		return true
	}

	// Check for common words (basic similarity)
	words1 := strings.Fields(name1)
	words2 := strings.Fields(name2)

	commonWords := 0
	for _, word1 := range words1 {
		for _, word2 := range words2 {
			if word1 == word2 && len(word1) > 2 { // Ignore short words
				commonWords++
			}
		}
	}

	// Consider similar if they have at least 1 common meaningful word
	return commonWords > 0
}
