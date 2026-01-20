package storage

import (
	"context"
	"log/slog"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/account"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/asset"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/holding"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/portfolio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateHolding creates a new holding record.
func (s *StorageService) CreateHolding(ctx context.Context, req *services.CreateHoldingRequest) (*models.Holding, error) {
	if req.Holding == nil || req.Holding.AssetId == "" || req.Holding.AccountId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "holding with asset_id and optional portfolio_id or account_id is required")
	}

	createHolding := s.dbClient.Holding.Create()

	assetUUID, err := stringToUUID(req.Holding.AssetId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "bad asset_id")
	}
	entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "asset not found")
	}
	createHolding = createHolding.SetAsset(entAsset)

	if req.Holding.PortfolioId != nil {
		portfolioUUID, err := stringToUUID(*req.Holding.PortfolioId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "bad portfolio_id")
		}
		entPortfolio, err := s.dbClient.Portfolio.Query().Where(portfolio.UUID(portfolioUUID)).Only(ctx)
		if err != nil {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}

		createHolding = createHolding.SetPortfolio(entPortfolio)
	}

	if req.Holding.AccountId != "" {
		accountUUID, err := stringToUUID(req.Holding.AccountId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "bad account_id")
		}
		entAccount, err := s.dbClient.Account.Query().Where(account.UUID(accountUUID)).Only(ctx)
		if err != nil {
			return nil, status.Error(codes.NotFound, "account not found")
		}

		createHolding = createHolding.SetAccount(entAccount)
	}

	entHolding, err := createHolding.
		SetAmount(req.Holding.Amount).
		SetDecimals(req.Holding.Decimals).
		Save(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create holding")
	}

	entHolding, err = s.dbClient.Holding.Query().
		Where(holding.ID(entHolding.ID)).
		WithAsset().
		WithPortfolio().
		WithAccount().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "holding not found")
		}
		return nil, status.Errorf(codes.Internal, "db error: %v", err)
	}

	return entHoldingToProtoHolding(entHolding)
}

// GetHolding returns a holding by ID.
func (s *StorageService) GetHolding(ctx context.Context, req *services.GetHoldingRequest) (*models.Holding, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "holding ID is required")
	}
	uuidVal, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad holding ID")
	}

	entHolding, err := s.dbClient.Holding.Query().
		Where(holding.UUID(uuidVal)).
		WithAsset().
		WithPortfolio().
		WithAccount().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "holding not found")
		}
		return nil, status.Errorf(codes.Internal, "db error: %v", err)
	}

	return entHoldingToProtoHolding(entHolding)
}

// UpdateHolding updates fields on a holding using a fieldmask.
func (s *StorageService) UpdateHolding(ctx context.Context, req *services.UpdateHoldingRequest) (*models.Holding, error) {
	if req.Holding == nil || req.Holding.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "holding with ID is required")
	}
	if req.UpdateMask == nil || len(req.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required for update operation")
	}

	holdingUUID, err := stringToUUID(req.Holding.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid holding ID format: %v", err)
	}

	entHolding, err := s.dbClient.Holding.Query().Where(holding.UUID(holdingUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "holding with ID %s not found", req.Holding.Id)
		}
		s.log.Error("Failed to get holding", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve holding: %v", err)
	}

	mutation := entHolding.Update()
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "amount":
			mutation.SetAmount(req.Holding.Amount)
		case "Decimals":
			mutation.SetDecimals(req.Holding.Decimals)
		case "asset_id":
			if req.Holding.AssetId == "" {
				return nil, status.Errorf(codes.InvalidArgument, "asset_id cannot be empty")
			}
			assetUUID, err := stringToUUID(req.Holding.AssetId)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "bad asset_id")
			}
			entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "asset not found")
			}
			mutation.SetAsset(entAsset)
		case "portfolio_id":
			if req.Holding.PortfolioId == nil {
				return nil, status.Errorf(codes.InvalidArgument, "portfolio_id cannot be empty")
			}
			portfolioUUID, err := stringToUUID(*req.Holding.PortfolioId)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "bad portfolio_id")
			}
			entPortfolio, err := s.dbClient.Portfolio.Query().Where(portfolio.UUID(portfolioUUID)).Only(ctx)
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "portfolio not found")
			}
			mutation.SetPortfolio(entPortfolio)
		case "account_id":
			if req.Holding.AccountId == "" {
				return nil, status.Errorf(codes.InvalidArgument, "account_id cannot be empty")
			}
			accountUUID, err := stringToUUID(req.Holding.AccountId)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "bad account_id")
			}
			entAccount, err := s.dbClient.Account.Query().Where(account.UUID(accountUUID)).Only(ctx)
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "account not found")
			}
			mutation.SetAccount(entAccount)
		default:
			s.log.Warn("UpdateHolding requested with unknown field in mask", slog.String("path", path))
		}
	}

	if _, err := mutation.Save(ctx); err != nil {
		s.log.Error("Failed to update holding", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to update holding: %v", err)
	}
	entHolding, err = s.dbClient.Holding.Query().
		Where(holding.UUID(holdingUUID)).
		WithAsset().
		WithPortfolio().
		WithAccount().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "holding with ID %s not found", req.Holding.Id)
		}
		s.log.Error("Failed to get holding", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve holding: %v", err)
	}
	return entHoldingToProtoHolding(entHolding)
}

// ListHoldings lists holdings, filtered by portfolio/account/asset if provided.
func (s *StorageService) ListHoldings(ctx context.Context, req *services.ListHoldingsRequest) (*services.ListHoldingsResponse, error) {
	query := s.dbClient.Holding.Query().
		WithAsset().
		WithPortfolio().
		WithAccount()

	if req.PortfolioId != nil {
		portfolioUUID, err := stringToUUID(*req.PortfolioId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad portfolio_id")
		}
		entPortfolioObj, err := s.dbClient.Portfolio.Query().Where(portfolio.UUID(portfolioUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "portfolio not found")
		}
		query = query.Where(holding.PortfolioID(entPortfolioObj.ID))
	}
	if req.AccountId != nil {
		accountUUID, err := stringToUUID(*req.AccountId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad account_id")
		}
		entAccount, err := s.dbClient.Account.Query().Where(account.UUID(accountUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		query = query.Where(holding.AccountID(entAccount.ID))
	}
	if req.AssetId != nil {
		assetUUID, err := stringToUUID(*req.AssetId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad asset_id")
		}
		entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "asset not found")
		}
		query = query.Where(holding.AssetID(entAsset.ID))
	}

	limit := DefaultPageSize
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
	}
	// TODO: implement cursor-based pagination if required

	query = query.Limit(limit + 1)

	entHoldings, err := query.All(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list holdings: %v", err)
	}

	protoHoldings := make([]*models.Holding, 0, len(entHoldings))
	for i, entHolding := range entHoldings {
		if i == limit {
			break
		}
		protoHolding, err := entHoldingToProtoHolding(entHolding)
		if err != nil {
			continue
		}
		protoHoldings = append(protoHoldings, protoHolding)
	}

	var nextPageToken string
	if len(entHoldings) > limit {
		// pagination: using holding UUID string
		last := entHoldings[limit-1]
		nextPageToken = last.UUID.String()
	}

	return &services.ListHoldingsResponse{
		Holdings:      protoHoldings,
		NextPageToken: nextPageToken,
	}, nil
}
