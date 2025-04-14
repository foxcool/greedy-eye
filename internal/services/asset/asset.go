package asset

import (
	"github.com/foxcool/greedy-eye/internal/api/services"
)

// Service implements the AssetService gRPC service.
type Service struct {
	services.UnimplementedAssetServiceServer
	// ToDo: Add dependencies, such as StorageClient, logger, etc.
}

// NewService creates a new AssetService.
func NewService( /* dependencies */ ) *Service {
	return &Service{}
}
