package storage

import (
	"context"
	"log/slog"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/portfolio"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// CreatePortfolio creates a new portfolio for a user.
func (s *StorageService) CreatePortfolio(ctx context.Context, req *services.CreatePortfolioRequest) (*models.Portfolio, error) {
	if req.Portfolio == nil {
		return nil, status.Errorf(codes.InvalidArgument, "portfolio information is required")
	}
	if req.Portfolio.UserId == "" || req.Portfolio.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "user_id and name are required")
	}
	userUUID, err := stringToUUID(req.Portfolio.UserId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id format: %v", err)
	}
	entUser, err := s.dbClient.User.Query().Where(user.UUID(userUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "user not found: %v", req.Portfolio.UserId)
	}

	create := s.dbClient.Portfolio.
		Create().
		SetUserID(entUser.ID).
		SetName(req.Portfolio.Name).
		SetNillableDescription(req.Portfolio.Description)
	entPortfolio, err := create.Save(ctx)
	if err != nil {
		s.log.Error("Failed to create portfolio", slog.Any("error",err))
		if ent.IsConstraintError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "portfolio creation constraint failed: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create portfolio: %v", err)
	}

	entPortfolio, err = s.dbClient.Portfolio.Query().Where(portfolio.ID(entPortfolio.ID)).WithUser().Only(ctx)
	if err != nil {
		s.log.Error("Failed to get created portfolio", slog.String("uuid", entPortfolio.UUID.String()), slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve portfolio: %v", err)
	}

	protoPortfolio, err := entPortfolioToProtoPortfolio(entPortfolio)
	if err != nil {
		s.log.Error("Failed to convert portfolio to proto", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to convert portfolio to proto: %v", err)
	}
	return protoPortfolio, nil
}

// GetPortfolio retrieves a portfolio by ID.
func (s *StorageService) GetPortfolio(ctx context.Context, req *services.GetPortfolioRequest) (*models.Portfolio, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "portfolio ID is required")
	}
	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid portfolio ID format: %v", err)
	}
	port, err := s.dbClient.Portfolio.Query().Where(portfolio.UUID(parsedUUID)).WithUser().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Portfolio not found", slog.String("uuid", req.Id))
			return nil, status.Errorf(codes.NotFound, "portfolio with ID %s not found", req.Id)
		}
		s.log.Error("Failed to get portfolio", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve portfolio: %v", err)
	}
	protoPortfolio, err := entPortfolioToProtoPortfolio(port)
	if err != nil {
		s.log.Error("Failed to convert portfolio to proto", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to convert portfolio: %v", err)
	}
	return protoPortfolio, nil
}

// UpdatePortfolio updates fields on a portfolio using a field mask.
func (s *StorageService) UpdatePortfolio(ctx context.Context, req *services.UpdatePortfolioRequest) (*models.Portfolio, error) {
	if req.Portfolio == nil || req.Portfolio.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "portfolio with ID is required")
	}
	if req.UpdateMask == nil || len(req.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required for update operation")
	}
	parsedUUID, err := stringToUUID(req.Portfolio.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid portfolio ID format: %v", err)
	}

	entPortfolio, err := s.dbClient.Portfolio.Query().Where(portfolio.UUID(parsedUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Portfolio not found", slog.String("uuid", req.Portfolio.Id))
			return nil, status.Errorf(codes.NotFound, "portfolio with ID %s not found", req.Portfolio.Id)
		}

		s.log.Error("Failed to get portfolio", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve portfolio: %v", err)
	}

	mutation := entPortfolio.Update()
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "name":
			if req.Portfolio.Name == "" {
				return nil, status.Errorf(codes.InvalidArgument, "name cannot be empty")
			}
			mutation.SetName(req.Portfolio.Name)
		case "description":
			mutation.SetNillableDescription(req.Portfolio.Description)
		default:
			s.log.Warn("UpdatePortfolio unknown field", slog.String("path", path))
		}
	}
	if _, err := mutation.Save(ctx); err != nil {
		s.log.Error("Failed to update portfolio", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to update portfolio: %v", err)
	}

	entPortfolio, err = s.dbClient.Portfolio.Query().Where(portfolio.UUID(parsedUUID)).WithUser().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Portfolio not found", slog.String("uuid", req.Portfolio.Id))
			return nil, status.Errorf(codes.NotFound, "portfolio with ID %s not found", req.Portfolio.Id)
		}

		s.log.Error("Failed to get portfolio", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve portfolio: %v", err)
	}
	return entPortfolioToProtoPortfolio(entPortfolio)
}

// DeletePortfolio deletes a portfolio by ID.
func (s *StorageService) DeletePortfolio(ctx context.Context, req *services.DeletePortfolioRequest) (*emptypb.Empty, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "portfolio ID is required")
	}
	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid portfolio ID format: %v", err)
	}
	delCount, err := s.dbClient.Portfolio.Delete().Where(portfolio.UUID(parsedUUID)).Exec(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			s.log.Error("Failed to delete portfolio due to constraint", slog.Any("error",err))
			return nil, status.Errorf(codes.FailedPrecondition, "cannot delete portfolio due to existing dependencies: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to delete portfolio: %v", err)
	}
	if delCount == 0 {
		return nil, status.Errorf(codes.NotFound, "portfolio with ID %s not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// ListPortfolios lists portfolios by user_id (if provided), with pagination.
func (s *StorageService) ListPortfolios(ctx context.Context, req *services.ListPortfoliosRequest) (*services.ListPortfoliosResponse, error) {
	query := s.dbClient.Portfolio.Query().Order(ent.Asc(portfolio.FieldCreatedAt)).WithUser()
	if req.UserId != nil && *req.UserId != "" {
		userUUID, err := stringToUUID(*req.UserId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad user_id")
		}
		entUser, err := s.dbClient.User.Query().Where(user.UUID(userUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "user not found")
		}
		query = query.Where(portfolio.UserID(entUser.ID))
	}
	limit := DefaultPageSize
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
	}
	var cursorUUID string
	if req.PageToken != nil && *req.PageToken != "" {
		cursorUUID = *req.PageToken
	}
	if cursorUUID != "" {
		uuidVal, err := stringToUUID(cursorUUID)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid cursor UUID: %v", err)
		}
		query = query.Where(portfolio.UUIDGT(uuidVal))
	}
	query = query.Limit(limit + 1)

	portfolios, err := query.All(ctx)
	if err != nil {
		s.log.Error("Failed to list portfolios", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to list portfolios: %v", err)
	}
	protos := make([]*models.Portfolio, 0, len(portfolios))
	for i, entPortfolio := range portfolios {
		if i == limit {
			break
		}
		proto, err := entPortfolioToProtoPortfolio(entPortfolio)
		if err != nil {
			continue
		}
		protos = append(protos, proto)
	}
	var nextPageToken string
	if len(portfolios) > limit {
		last := portfolios[limit-1]
		nextPageToken = last.UUID.String()
	}
	return &services.ListPortfoliosResponse{
		Portfolios:    protos,
		NextPageToken: nextPageToken,
	}, nil
}
