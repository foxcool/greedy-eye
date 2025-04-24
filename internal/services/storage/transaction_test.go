package storage

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func createTestTransaction(t *testing.T, storageService *StorageService, accountId string, txType models.TransactionType, status models.TransactionStatus) *models.Transaction {
	req := &services.CreateTransactionRequest{
		Transaction: &models.Transaction{
			Type:      txType,
			Status:    status,
			AccountId: accountId,
			Data: map[string]string{
				"note": "abc",
			},
		},
	}
	tx, err := storageService.CreateTransaction(t.Context(), req)
	if !assert.NoError(t, err) || !assert.NotNil(t, tx) {
		assert.FailNow(t, "transaction creation failed")
	}
	return tx
}

func TestCreateTransaction(t *testing.T) {
	storageSrvice := getTransactionedService(t)
	user := createTestUser(t, storageSrvice, "TestUser", "test@test.test")
	account := createTestAccount(t, storageSrvice, user.Id, "TestAccount", models.AccountType_ACCOUNT_TYPE_BANK)

	t.Run("Create transaction with valid fields", func(t *testing.T) {
		req := &services.CreateTransactionRequest{
			Transaction: &models.Transaction{
				Type:      models.TransactionType_TRANSACTION_TYPE_TRADE,
				Status:    models.TransactionStatus_TRANSACTION_STATUS_PENDING,
				AccountId: account.Id,
				Data: map[string]string{
					"note": "abc",
				},
			},
		}
		tx, err := storageSrvice.CreateTransaction(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, tx) {
			assert.FailNow(t, "transaction creation failed")
		}
		assert.NotEmpty(t, tx.Id)
		assert.NotEmpty(t, tx.CreatedAt)
		assert.Equal(t, req.Transaction.Type, tx.Type)
		assert.Equal(t, req.Transaction.Status, tx.Status)
		assert.Equal(t, account.Id, tx.AccountId)
		assert.EqualValues(t, req.Transaction.Data, tx.Data)
	})

	t.Run("Create transaction with missing required fields", func(t *testing.T) {
		req := &services.CreateTransactionRequest{
			Transaction: &models.Transaction{
				Type:   models.TransactionType_TRANSACTION_TYPE_TRADE,
				Status: models.TransactionStatus_TRANSACTION_STATUS_PENDING,
			},
		}
		tx, err := storageSrvice.CreateTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, tx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Create transaction with invalid account ID", func(t *testing.T) {
		req := &services.CreateTransactionRequest{
			Transaction: &models.Transaction{
				Type:      models.TransactionType_TRANSACTION_TYPE_TRADE,
				Status:    models.TransactionStatus_TRANSACTION_STATUS_PENDING,
				AccountId: "invalid-account-id",
			},
		}
		tx, err := storageSrvice.CreateTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, tx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Create transaction with not found account", func(t *testing.T) {
		req := &services.CreateTransactionRequest{
			Transaction: &models.Transaction{
				Type:      models.TransactionType_TRANSACTION_TYPE_TRADE,
				Status:    models.TransactionStatus_TRANSACTION_STATUS_PENDING,
				AccountId: uuid.New().String(),
			},
		}
		tx, err := storageSrvice.CreateTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, tx)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestGetTransaction(t *testing.T) {
	storageSrvice := getTransactionedService(t)
	user := createTestUser(t, storageSrvice, "TestUser", "test@test.test")
	account := createTestAccount(t, storageSrvice, user.Id, "TestAccount", models.AccountType_ACCOUNT_TYPE_BANK)
	tx := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_TRADE, models.TransactionStatus_TRANSACTION_STATUS_PENDING)

	t.Run("Get transaction with valid ID", func(t *testing.T) {
		tx, err := storageSrvice.GetTransaction(t.Context(), &services.GetTransactionRequest{Id: tx.Id})
		if !assert.NoError(t, err) || !assert.NotNil(t, tx) {
			assert.FailNow(t, "transaction retrieval failed")
		}
		assert.Equal(t, tx.Id, tx.Id)
		assert.Equal(t, tx.Type, tx.Type)
		assert.Equal(t, tx.Status, tx.Status)
		assert.Equal(t, account.Id, tx.AccountId)
	})

	t.Run("Get transaction with invalid ID", func(t *testing.T) {
		req := &services.GetTransactionRequest{Id: "invalid-id"}
		tx, err := storageSrvice.GetTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, tx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Get transaction with not found ID", func(t *testing.T) {
		req := &services.GetTransactionRequest{Id: uuid.New().String()}
		tx, err := storageSrvice.GetTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, tx)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestUpdateTransaction(t *testing.T) {
	storageSrvice := getTransactionedService(t)
	user := createTestUser(t, storageSrvice, "TestUser", "test@test.test")
	account := createTestAccount(t, storageSrvice, user.Id, "TestAccount", models.AccountType_ACCOUNT_TYPE_BANK)
	tx := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_TRADE, models.TransactionStatus_TRANSACTION_STATUS_PENDING)

	t.Run("Update status", func(t *testing.T) {
		req := &services.UpdateTransactionRequest{
			Transaction: &models.Transaction{
				Id:     tx.Id,
				Status: models.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}
		updated, err := storageSrvice.UpdateTransaction(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, updated) {
			assert.FailNow(t, "transaction update failed")
		}
		assert.Equal(t, req.Transaction.Status, updated.Status)
		assert.Equal(t, tx.Id, updated.Id)
	})

	t.Run("Update type and data", func(t *testing.T) {
		req := &services.UpdateTransactionRequest{
			Transaction: &models.Transaction{
				Id:   tx.Id,
				Type: models.TransactionType_TRANSACTION_TYPE_TRADE,
				Data: map[string]string{"status": "updated"},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"type", "data"}},
		}
		updated, err := storageSrvice.UpdateTransaction(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, updated) {
			assert.FailNow(t, "transaction update failed")
		}
		assert.Equal(t, tx.Id, updated.Id)
		assert.Equal(t, req.Transaction.Type, updated.Type)
		assert.Equal(t, account.Id, updated.AccountId)
		assert.EqualValues(t, req.Transaction.Data, updated.Data)
	})

	t.Run("Update with not found Account ID", func(t *testing.T) {
		req := &services.UpdateTransactionRequest{
			Transaction: &models.Transaction{
				Id:        tx.Id,
				AccountId: uuid.New().String(),
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"account_id"}},
		}
		updated, err := storageSrvice.UpdateTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, updated)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Update with invalid ID", func(t *testing.T) {
		req := &services.UpdateTransactionRequest{
			Transaction: &models.Transaction{
				Id: uuid.New().String(),
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}
		updated, err := storageSrvice.UpdateTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, updated)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Update with empty ID", func(t *testing.T) {
		req := &services.UpdateTransactionRequest{
			Transaction: &models.Transaction{
				Id: "",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}
		updated, err := storageSrvice.UpdateTransaction(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, updated)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestListTransactions(t *testing.T) {
	storageSrvice := getTransactionedService(t)
	user := createTestUser(t, storageSrvice, "TestUser", "test@test.test")
	account := createTestAccount(t, storageSrvice, user.Id, "TestAccount", models.AccountType_ACCOUNT_TYPE_BANK)
	tx0 := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_DEPOSIT, models.TransactionStatus_TRANSACTION_STATUS_PENDING)
	tx1 := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_TRANSFER, models.TransactionStatus_TRANSACTION_STATUS_COMPLETED)
	tx2 := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_EXTENDED, models.TransactionStatus_TRANSACTION_STATUS_FAILED)
	tx3 := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_TRADE, models.TransactionStatus_TRANSACTION_STATUS_PENDING)
	tx4 := createTestTransaction(t, storageSrvice, account.Id, models.TransactionType_TRANSACTION_TYPE_WITHDRAWAL, models.TransactionStatus_TRANSACTION_STATUS_COMPLETED)

	t.Run("List transactions by account ID", func(t *testing.T) {
		txs, err := storageSrvice.ListTransactions(t.Context(), &services.ListTransactionsRequest{
			AccountId: &account.Id,
		})
		if !assert.NoError(t, err) || !assert.NotNil(t, txs) {
			assert.FailNow(t, "transaction listing failed")
		}
		assert.Len(t, txs.Transactions, 5)
		assert.Equal(t, tx0.Id, txs.Transactions[0].Id)
		assert.Equal(t, tx1.Id, txs.Transactions[1].Id)
		assert.Equal(t, tx2.Id, txs.Transactions[2].Id)
		assert.Equal(t, tx3.Id, txs.Transactions[3].Id)
		assert.Equal(t, tx4.Id, txs.Transactions[4].Id)
	})

	t.Run("List transactions by type", func(t *testing.T) {
		txType := models.TransactionType_TRANSACTION_TYPE_DEPOSIT
		txs, err := storageSrvice.ListTransactions(t.Context(), &services.ListTransactionsRequest{
			Type: &txType,
		})
		if !assert.NoError(t, err) || !assert.NotNil(t, txs) {
			assert.FailNow(t, "transaction listing failed")
		}
		assert.Len(t, txs.Transactions, 1)
		assert.Equal(t, txType, txs.Transactions[0].Type)
	})

	t.Run("List transactions by status", func(t *testing.T) {
		txStatus := models.TransactionStatus_TRANSACTION_STATUS_PENDING
		txs, err := storageSrvice.ListTransactions(t.Context(), &services.ListTransactionsRequest{
			Status: &txStatus,
		})
		if !assert.NoError(t, err) || !assert.NotNil(t, txs) {
			assert.FailNow(t, "transaction listing failed")
		}
		assert.Equal(t, txStatus, txs.Transactions[0].Status)
	})

	t.Run("List transactions with pagination", func(t *testing.T) {
		pageSize := int32(2)
		txs, err := storageSrvice.ListTransactions(t.Context(), &services.ListTransactionsRequest{
			AccountId: &account.Id,
			PageSize:  &pageSize,
		})
		if !assert.NoError(t, err) || !assert.NotNil(t, txs) {
			assert.FailNow(t, "transaction listing failed")
		}
		assert.Len(t, txs.Transactions, 2)
		assert.Equal(t, tx0.Id, txs.Transactions[0].Id)
		assert.Equal(t, tx1.Id, txs.Transactions[1].Id)
		assert.NotEmpty(t, txs.NextPageToken)
	})

	t.Run("List transactions with invalid account ID", func(t *testing.T) {
		txs, err := storageSrvice.ListTransactions(t.Context(), &services.ListTransactionsRequest{
			AccountId: &[]string{"invalid-account-id"}[0],
		})
		assert.Error(t, err)
		assert.Nil(t, txs)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("List transactions with not found account ID", func(t *testing.T) {
		txs, err := storageSrvice.ListTransactions(t.Context(), &services.ListTransactionsRequest{
			AccountId: &[]string{uuid.New().String()}[0],
		})
		assert.Error(t, err)
		assert.Nil(t, txs)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}
