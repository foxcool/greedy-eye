package storage

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/account"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/transaction"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateTransaction creates a new transaction record.
func (s *StorageService) CreateTransaction(ctx context.Context, req *services.CreateTransactionRequest) (*models.Transaction, error) {
	if req.Transaction == nil {
		return nil, status.Errorf(codes.InvalidArgument, "transaction data is required")
	}

	entType, err := protoTransactionTypeToEnt(req.Transaction.Type)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid transaction type: %v", req.Transaction.Type)
	}
	entStatus, err := protoTransactionStatusToEnt(req.Transaction.Status)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid transaction status: %v", req.Transaction.Status)
	}

	accountUUID, err := stringToUUID(req.Transaction.AccountId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid account_id format: %v", err)
	}
	entAccount, err := s.dbClient.Account.Query().Where(account.UUID(accountUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "account not found: %v", req.Transaction.AccountId)
		}
		return nil, status.Errorf(codes.Internal, "failed to get account: %v", err)
	}

	createTx := s.dbClient.Transaction.
		Create().
		SetType(entType).
		SetStatus(entStatus).
		SetAccount(entAccount).
		SetData(req.Transaction.Data)

	if req.Transaction.CreatedAt != nil {
		createTx.SetCreatedAt(req.Transaction.CreatedAt.AsTime())
	}
	if req.Transaction.UpdatedAt != nil {
		createTx.SetUpdatedAt(req.Transaction.UpdatedAt.AsTime())
	}
	entTx, err := createTx.Save(ctx)
	if err != nil {
		s.log.Error("Failed to create transaction", zap.Error(err))
		if ent.IsConstraintError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "transaction constraint failed: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create transaction: %v", err)
	}

	entTx, err = s.dbClient.Transaction.Query().Where(transaction.ID(entTx.ID)).WithAccount().Only(ctx)
	if err != nil {
		s.log.Error("Failed to get created transaction", zap.String("uuid", entTx.UUID.String()), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve transaction: %v", err)
	}

	protoTx, err := entTransactionToProtoTransaction(entTx)
	if err != nil {
		s.log.Error("Failed to convert transaction to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert transaction to proto: %v", err)
	}

	return protoTx, nil
}

// GetTransaction retrieves a transaction by its ID.
func (s *StorageService) GetTransaction(ctx context.Context, req *services.GetTransactionRequest) (*models.Transaction, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "transaction ID is required")
	}
	uuidVal, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad transaction ID")
	}

	entTx, err := s.dbClient.Transaction.Query().Where(transaction.UUID(uuidVal)).WithAccount().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "transaction not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get transaction: %v", err)
	}

	return entTransactionToProtoTransaction(entTx)
}

// UpdateTransaction updates a transaction's fields via field mask.
func (s *StorageService) UpdateTransaction(ctx context.Context, req *services.UpdateTransactionRequest) (*models.Transaction, error) {
	if req.Transaction == nil || req.Transaction.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "transaction with ID is required")
	}
	if req.UpdateMask == nil || len(req.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required")
	}

	txUUID, err := stringToUUID(req.Transaction.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid transaction ID format: %v", err)
	}

	entTx, err := s.dbClient.Transaction.Query().Where(transaction.UUID(txUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "transaction with ID %s not found", req.Transaction.Id)
		}
		s.log.Error("Failed to get transaction", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve transaction: %v", err)
	}

	mutation := entTx.Update()
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "status":
			entStatus, err := protoTransactionStatusToEnt(req.Transaction.Status)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid status: %v", req.Transaction.Status)
			}
			mutation.SetStatus(entStatus)
		case "type":
			entType, err := protoTransactionTypeToEnt(req.Transaction.Type)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid type: %v", req.Transaction.Type)
			}
			mutation.SetType(entType)
		case "data":
			if req.Transaction.Data == nil {
				return nil, status.Errorf(codes.InvalidArgument, "data cannot be nil")
			}
			mutation.SetData(req.Transaction.Data)
		case "account_id":
			if req.Transaction.AccountId == "" {
				return nil, status.Errorf(codes.InvalidArgument, "account_id required in mask")
			}
			accountUUID, err := stringToUUID(req.Transaction.AccountId)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "bad account_id")
			}
			entAccount, err := s.dbClient.Account.Query().Where(account.UUID(accountUUID)).Only(ctx)
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "account not found")
			}
			mutation.SetAccount(entAccount)
		default:
			s.log.Warn("UpdateTransaction: unknown field in mask", zap.String("path", path))
		}
	}
	if _, err := mutation.Save(ctx); err != nil {
		s.log.Error("Failed to update transaction", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update transaction: %v", err)
	}
	entTx, err = s.dbClient.Transaction.Query().Where(transaction.UUID(txUUID)).WithAccount().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "transaction with ID %s not found", req.Transaction.Id)
		}
		s.log.Error("Failed to get transaction", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve transaction: %v", err)
	}
	protoTx, err := entTransactionToProtoTransaction(entTx)
	if err != nil {
		s.log.Error("Failed to convert transaction to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert transaction to proto: %v", err)
	}
	return protoTx, nil
}

// ListTransactions lists transactions with optional filtering by portfolio/account/asset/type/status/time.
func (s *StorageService) ListTransactions(ctx context.Context, req *services.ListTransactionsRequest) (*services.ListTransactionsResponse, error) {
	query := s.dbClient.Transaction.Query()

	if req.AccountId != nil && *req.AccountId != "" {
		accountUUID, err := stringToUUID(*req.AccountId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad account_id")
		}
		entAccount, err := s.dbClient.Account.Query().Where(account.UUID(accountUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "account not found")
		}
		query = query.Where(transaction.AccountID(entAccount.ID))
	}

	if req.Type != nil {
		entType, err := protoTransactionTypeToEnt(*req.Type)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad type")
		}
		query = query.Where(transaction.TypeEQ(entType))
	}

	if req.Status != nil {
		entStatus, err := protoTransactionStatusToEnt(*req.Status)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad status")
		}
		query = query.Where(transaction.StatusEQ(entStatus))
	}

	if req.From != nil {
		query = query.Where(transaction.CreatedAtGTE(req.From.AsTime()))
	}

	if req.To != nil {
		query = query.Where(transaction.CreatedAtLTE(req.To.AsTime()))
	}

	limit := DefaultPageSize
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
	}
	query = query.Order(ent.Asc(transaction.FieldCreatedAt)).Limit(limit + 1)

	// Pagination by page_token (base64 encoded timestamp)
	var cursorTs time.Time
	if req.PageToken != nil && *req.PageToken != "" {
		raw, _ := base64.StdEncoding.DecodeString(*req.PageToken)
		if len(raw) > 0 {
			err := cursorTs.UnmarshalText(raw)
			if err == nil {
				query = query.Where(transaction.CreatedAt(cursorTs))
			}
		}
	}

	entTxs, err := query.WithAccount().All(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list transactions: %v", err)
	}

	protoTxs := make([]*models.Transaction, 0, len(entTxs))
	for i, entTx := range entTxs {
		if i == limit {
			break
		}

		protoTx, err := entTransactionToProtoTransaction(entTx)
		if err == nil {
			protoTxs = append(protoTxs, protoTx)
		}
	}

	var nextPageToken string
	if len(entTxs) > limit {
		last := entTxs[limit-1]
		txt, _ := last.CreatedAt.MarshalText()
		nextPageToken = base64.StdEncoding.EncodeToString(txt)
	}

	return &services.ListTransactionsResponse{
		Transactions:  protoTxs,
		NextPageToken: nextPageToken,
	}, nil
}
