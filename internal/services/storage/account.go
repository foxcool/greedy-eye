package storage

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/account"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/user"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *StorageService) CreateAccount(ctx context.Context, req *services.CreateAccountRequest) (*models.Account, error) {
	if req.Account == nil {
		return nil, status.Errorf(codes.InvalidArgument, "account information is required")
	}
	if req.Account.UserId == "" || req.Account.Name == "" || req.Account.Type == models.AccountType_ACCOUNT_TYPE_UNSPECIFIED {
		return nil, status.Errorf(codes.InvalidArgument, "user_id, name, type required")
	}

	entType, err := protoAccountTypeToEnt(req.Account.Type)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account type: %v", req.Account.Type)
	}

	userUUID, err := stringToUUID(req.Account.UserId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id format: %v", err)
	}
	entUser, err := s.dbClient.User.Query().Where(user.UUID(userUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "user not found: %v", req.Account.UserId)
	}

	created, err := s.dbClient.Account.
		Create().
		SetUser(entUser).
		SetName(req.Account.Name).
		SetType(entType).
		SetData(req.Account.Data).
		SetNillableDescription(req.Account.Description).
		Save(ctx)
	if err != nil {
		s.log.Error("Failed to create account", zap.Error(err))
		if ent.IsConstraintError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "account creation constraint failed: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	account, err := s.dbClient.Account.Query().Where(account.ID(created.ID)).WithUser().Only(ctx)
	if err != nil {
		s.log.Error("Can't get createed account", zap.String("uuid", created.UUID.String()), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	protoAcccount, err := entAccountToProtoAccount(account)
	if err != nil {
		s.log.Error("Failed to convert account to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert account to proto: %v", err)
	}

	return protoAcccount, nil
}

func (s *StorageService) GetAccount(ctx context.Context, req *services.GetAccountRequest) (*models.Account, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account ID is required")
	}

	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account ID format: %v", err)
	}

	account, err := s.dbClient.Account.Query().Where(account.UUID(parsedUUID)).WithUser().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Account not found", zap.String("uuid", req.Id))
			return nil, status.Errorf(codes.NotFound, "account with ID %s not found", req.Id)
		}

		s.log.Error("Failed to get account", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	protoAccount, err := entAccountToProtoAccount(account)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert account: %v", err)
	}
	return protoAccount, nil
}

func (s *StorageService) UpdateAccount(ctx context.Context, req *services.UpdateAccountRequest) (*models.Account, error) {
	if req.Account == nil || req.Account.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account with ID is required")
	}
	if req.UpdateMask == nil || len(req.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required for update operation")
	}
	parsedUUID, err := stringToUUID(req.Account.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account ID format: %v", err)
	}

	entAccount, err := s.dbClient.Account.Query().Where(account.UUID(parsedUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Account not found", zap.String("uuid", req.Account.Id))
			return nil, status.Errorf(codes.NotFound, "account with ID %s not found", req.Account.Id)
		}

		s.log.Error("Failed to get account", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	mutation := entAccount.Update()
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "name":
			if req.Account.Name == "" {
				return nil, status.Errorf(codes.InvalidArgument, "name cannot be empty")
			}
			mutation.SetName(req.Account.Name)
		case "description":
			mutation.SetNillableDescription(req.Account.Description)
		case "type":
			entType, errx := protoAccountTypeToEnt(req.Account.Type)
			if errx != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid type: %v", req.Account.Type)
			}
			mutation.SetType(entType)
		case "data":
			mutation.SetData(req.Account.Data)
		default:
			s.log.Warn("UpdateAccount unknown field", zap.String("path", path))
		}
	}
	_, err = mutation.Save(ctx)
	if err != nil {
		s.log.Error("Failed to update account", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update account: %v", err)
	}

	entAccount, err = s.dbClient.Account.Query().Where(account.UUID(parsedUUID)).WithUser().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Account not found", zap.String("uuid", req.Account.Id))
			return nil, status.Errorf(codes.NotFound, "account with ID %s not found", req.Account.Id)
		}

		s.log.Error("Failed to get account", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	return entAccountToProtoAccount(entAccount)
}

func (s *StorageService) DeleteAccount(ctx context.Context, req *services.DeleteAccountRequest) (*emptypb.Empty, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account ID is required")
	}
	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account ID format: %v", err)
	}
	delCount, err := s.dbClient.Account.Delete().Where(account.UUID(parsedUUID)).Exec(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			s.log.Error("Failed to delete account due to constraint", zap.Error(err))
			return nil, status.Errorf(codes.FailedPrecondition, "cannot delete account due to existing dependencies: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to delete account: %v", err)
	}
	if delCount == 0 {
		return nil, status.Errorf(codes.NotFound, "account with ID %s not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

func (s *StorageService) ListAccounts(ctx context.Context, req *services.ListAccountsRequest) (*services.ListAccountsResponse, error) {
	query := s.dbClient.Account.Query()

	// Optional filters
	if req.UserId != nil {
		userUUID, err := stringToUUID(*req.UserId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad user_id")
		}
		entUser, err := s.dbClient.User.Query().Where(user.UUID(userUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "user not found")
		}
		query = query.Where(account.UserID(entUser.ID))
	}
	if req.Type != nil {
		entType, err := protoAccountTypeToEnt(*req.Type)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad type")
		}
		query = query.Where(account.TypeEQ(entType))
	}

	limit := DefaultPageSize
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
	}
	query = query.Order(ent.Desc(account.FieldCreatedAt)).Limit(limit + 1)

	// Pagination by page_token (base64 encoded timestamp)
	var cursorTs time.Time
	if req.PageToken != nil && *req.PageToken != "" {
		raw, _ := base64.StdEncoding.DecodeString(*req.PageToken)
		if len(raw) > 0 {
			err := cursorTs.UnmarshalText(raw)
			if err == nil {
				query = query.Where(account.CreatedAt(cursorTs))
			}
		}
	}

	entAccounts, err := query.WithUser().All(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}
	protoAccounts := make([]*models.Account, 0, len(entAccounts))
	for i, entAccount := range entAccounts {
		if i == limit {
			break
		}
		protoAccount, err := entAccountToProtoAccount(entAccount)
		if err == nil {
			protoAccounts = append(protoAccounts, protoAccount)
		}
	}
	var nextPageToken string
	if len(entAccounts) > limit {
		last := entAccounts[limit-1]
		txt, _ := last.CreatedAt.MarshalText()
		nextPageToken = base64.StdEncoding.EncodeToString(txt)
	}

	return &services.ListAccountsResponse{
		Accounts:      protoAccounts,
		NextPageToken: nextPageToken,
	}, nil
}
