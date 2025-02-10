package asset

import grpc "google.golang.org/grpc"

// AssetService is main struct for asset service
type AssetService struct {
	UnimplementedAssetServiceServer
}

// NewAssetService creates new asset service
func NewAssetService() *AssetService {
	return &AssetService{}
}

// Register starts the server
func (s *AssetService) Register(server *grpc.Server) {
	RegisterAssetServiceServer(server, s)
}
