//go:build integration

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

func createTestAccount(t *testing.T, s *StorageService, userID, accountName string, accountType models.AccountType) *models.Account {
	account, err := s.CreateAccount(t.Context(), &services.CreateAccountRequest{
		Account: &models.Account{
			UserId: userID,
			Name:   accountName,
			Type:   accountType,
		},
	})
	if !assert.NoError(t, err) || !assert.NotNil(t, account) {
		assert.FailNow(t, "can't create account")
	}

	return account
}

func TestCreateAccount(t *testing.T) {
	storageService := getTransactionedService(t)

	// Create a user for testing
	user := createTestUser(t, storageService, "Vasiliy Petrov", "petrov@ya.ru")

	t.Run("Valid account creation", func(t *testing.T) {
		req := &services.CreateAccountRequest{Account: &models.Account{
			UserId: user.GetId(),
			Name:   "Test Account",
			Type:   models.AccountType_ACCOUNT_TYPE_WALLET,
		}}
		res, err := storageService.CreateAccount(t.Context(), req)
		if assert.NoError(t, err, "account creation should succeed") {
			assert.NotNil(t, res)
			assert.Equal(t, req.Account.UserId, res.UserId)
			assert.Equal(t, req.Account.Name, res.Name)
			assert.Equal(t, req.Account.Type, res.Type)
			assert.NotEmpty(t, res.Id)
			assert.NotEmpty(t, res.CreatedAt)
		}
	})

	t.Run("Missing required fields (name)", func(t *testing.T) {
		req := &services.CreateAccountRequest{Account: &models.Account{
			UserId: user.GetId(),
			Type:   models.AccountType_ACCOUNT_TYPE_WALLET,
		}}
		res, err := storageService.CreateAccount(t.Context(), req)
		if assert.Error(t, err, "account creation should fail") {
			assert.Nil(t, res)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		}
	})

	t.Run("Missing required fields (type)", func(t *testing.T) {
		req := &services.CreateAccountRequest{Account: &models.Account{
			UserId: user.GetId(),
			Name:   "Test Account 2",
		}}
		res, err := storageService.CreateAccount(t.Context(), req)
		if assert.Error(t, err, "account creation should fail") {
			assert.Nil(t, res)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		}
	})
}

func TestGetAccount(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "Alice", "alice@example.com")
	account := createTestAccount(t, storageService, user.GetId(), "Bank account", models.AccountType_ACCOUNT_TYPE_BANK)

	t.Run("Get existing account by ID", func(t *testing.T) {
		req := &services.GetAccountRequest{Id: account.Id}
		res, err := storageService.GetAccount(t.Context(), req)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "can't get account by ID")
		}
		assert.Equal(t, account.Id, res.Id)
		assert.Equal(t, account.Name, res.Name)
		assert.Equal(t, account.Type, res.Type)
		assert.Equal(t, account.UserId, res.UserId)
		assert.NotEmpty(t, res.CreatedAt)
	})

	t.Run("Get non-existent account", func(t *testing.T) {
		_, err := storageService.GetAccount(t.Context(), &services.GetAccountRequest{Id: uuid.New().String()})
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Get account with invalid ID format", func(t *testing.T) {
		account, err := storageService.GetAccount(t.Context(), &services.GetAccountRequest{Id: "bad-id-format"})
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, account)
	})

	t.Run("Get account with empty ID", func(t *testing.T) {
		account, err := storageService.GetAccount(t.Context(), &services.GetAccountRequest{})
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, account)
	})
}

func TestUpdateAccount(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "John Doe", "john@example.com")
	account := createTestAccount(t, storageService, user.Id, "New Exchange", models.AccountType_ACCOUNT_TYPE_EXCHANGE)

	t.Run("Update account type", func(t *testing.T) {
		req := &services.UpdateAccountRequest{
			Account: &models.Account{
				Id:   account.Id,
				Type: models.AccountType_ACCOUNT_TYPE_BANK,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"type"}},
		}
		res, err := storageService.UpdateAccount(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, res) {
			assert.FailNow(t, "can't update account type")
		}
		assert.Equal(t, account.Id, res.Id)
		assert.Equal(t, account.Name, res.Name)
		assert.NotEqual(t, account.Type, res.Type)
		assert.Equal(t, account.Data, res.Data)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, account.UpdatedAt)

		account = res
	})

	t.Run("Update account data", func(t *testing.T) {
		req := &services.UpdateAccountRequest{
			Account: &models.Account{
				Id:   account.Id,
				Data: map[string]string{"new": "data", "foo": "bar"},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data"}},
		}
		res, err := storageService.UpdateAccount(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, res) {
			assert.FailNow(t, "can't update account data")
		}
		assert.Equal(t, account.Id, res.Id)
		assert.Equal(t, account.Name, res.Name)
		assert.Equal(t, account.Type, res.Type)
		assert.EqualValues(t, req.Account.Data, res.Data)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, account.UpdatedAt)

		account = res
	})

	t.Run("Update with invalid type", func(t *testing.T) {
		req := &services.UpdateAccountRequest{
			Account: &models.Account{
				Id:   account.Id,
				Type: -10, // Not defined
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"type"}},
		}
		res, err := storageService.UpdateAccount(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Update non-existent account", func(t *testing.T) {
		nonexistentReq := &services.UpdateAccountRequest{
			Account: &models.Account{
				Id:   uuid.New().String(),
				Name: "Ghost Account",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdateAccount(t.Context(), nonexistentReq)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Missing update mask", func(t *testing.T) {
		req := &services.UpdateAccountRequest{
			Account:    &models.Account{Id: account.Id, Name: "Unused"},
			UpdateMask: nil,
		}
		res, err := storageService.UpdateAccount(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})
}

func TestDeleteAccount(t *testing.T) {
	storageService := getTransactionedService(t)

	user := createTestUser(t, storageService, "Barash", "barash@smeshariki.ru")
	account := createTestAccount(t, storageService, user.Id, "Barash BTC wallet", models.AccountType_ACCOUNT_TYPE_WALLET)

	t.Run("Valid delete", func(t *testing.T) {
		_, err := storageService.DeleteAccount(t.Context(), &services.DeleteAccountRequest{Id: account.Id})
		assert.NoError(t, err)
	})

	t.Run("Delete nonexistent", func(t *testing.T) {
		_, err := storageService.DeleteAccount(t.Context(), &services.DeleteAccountRequest{Id: uuid.New().String()})
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Invalid ID format", func(t *testing.T) {
		_, err := storageService.DeleteAccount(t.Context(), &services.DeleteAccountRequest{Id: "not-a-uuid"})
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Empty ID", func(t *testing.T) {
		_, err := storageService.DeleteAccount(t.Context(), &services.DeleteAccountRequest{Id: ""})
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}
