package price

import (
	context "context"

	"github.com/foxcool/greedy-eye/pkg/services/asset"
	grpc "google.golang.org/grpc"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type PricingService struct {
	UnimplementedPricingServiceServer
	AssetService AssetService
	PriceStorage PriceStorage
}

type AssetService interface {
	GetAsset(ctx context.Context, in *asset.AssetRequest) (*asset.AssetResponse, error)
	SetAsset(ctx context.Context, in *asset.AssetRequest) (*asset.AssetResponse, error)
	DeleteAsset(ctx context.Context, in *asset.AssetRequest) (*emptypb.Empty, error)
}

type PriceStorage interface {
}

func NewPricingService(assetService AssetService, priceStorage PriceStorage) *PricingService {
	return &PricingService{}
}

func (s *PricingService) Register(server *grpc.Server) {
	RegisterPricingServiceServer(server, s)
}
