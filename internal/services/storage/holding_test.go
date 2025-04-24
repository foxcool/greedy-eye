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

func createTestHolding(t *testing.T, storageService *StorageService, assetID, accountID, portfolioID string) *models.Holding {
	req := &services.CreateHoldingRequest{
		Holding: &models.Holding{
			AssetId:     assetID,
			AccountId:   accountID,
			PortfolioId: &portfolioID,
			Amount:      1234,
			Decimals:    6,
		},
	}
	res, err := storageService.CreateHolding(t.Context(), req)
	if !assert.NoError(t, err) && assert.NotEmpty(t, res.Id) {
		assert.FailNow(t, "holding is not created")
	}
	assert.Equal(t, req.Holding.AssetId, res.AssetId)
	assert.Equal(t, req.Holding.AccountId, res.AccountId)
	assert.Equal(t, req.Holding.Amount, res.Amount)
	assert.Equal(t, req.Holding.Decimals, res.Decimals)
	assert.NotEmpty(t, res.CreatedAt)
	assert.NotEmpty(t, res.Id)

	return res
}

func TestCreateHolding(t *testing.T) {
	storageService := getTransactionedService(t)

	// Create mandatory edges
	asset := createTestAsset(t, storageService, "Test")
	user := createTestUser(t, storageService, "TestUser", "test@company.org")
	account := createTestAccount(t, storageService, user.Id, "Test broker account", models.AccountType_ACCOUNT_TYPE_BROKER)
	portfolio := createTestPortfolio(t, storageService, user.Id)

	t.Run("create valid holding by account", func(t *testing.T) {
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AssetId:   asset.Id,
				AccountId: account.Id,
				Amount:    1234,
				Decimals:  6,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		if !assert.NoError(t, err) && assert.NotEmpty(t, res.Id) {
			assert.FailNow(t, "holding is not created")
		}
		assert.Equal(t, req.Holding.AssetId, res.AssetId)
		assert.Equal(t, req.Holding.AccountId, res.AccountId)
		assert.Equal(t, req.Holding.Amount, res.Amount)
		assert.Equal(t, req.Holding.Decimals, res.Decimals)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.Id)
	})

	t.Run("create valid holding with portfolio", func(t *testing.T) {
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AssetId:     asset.Id,
				AccountId:   account.Id,
				PortfolioId: &portfolio.Id,
				Amount:      4321,
				Decimals:    7,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		if !assert.NoError(t, err) && assert.NotEmpty(t, res.Id) {
			assert.FailNow(t, "holding is not created")
		}
		assert.Equal(t, req.Holding.AssetId, res.AssetId)
		assert.Equal(t, req.Holding.PortfolioId, res.PortfolioId)
		assert.Equal(t, req.Holding.Amount, res.Amount)
		assert.Equal(t, req.Holding.Decimals, res.Decimals)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.Id)
	})

	t.Run("create invalid holding without asset ID", func(t *testing.T) {
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AccountId: account.Id,
				Amount:    1,
				Decimals:  2,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("create invalid holding without account ID", func(t *testing.T) {
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AssetId:     asset.Id,
				PortfolioId: &portfolio.Id,
				Amount:      1,
				Decimals:    2,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("try to use asset ID that does not exist", func(t *testing.T) {
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AssetId:   uuid.New().String(),
				AccountId: account.Id,
				Amount:    2,
				Decimals:  2,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("try to use account ID that does not exist", func(t *testing.T) {
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AssetId:   asset.Id,
				AccountId: uuid.New().String(),
				Amount:    2,
				Decimals:  2,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("try to use portfolio ID that does not exist", func(t *testing.T) {
		randomUUID := uuid.New().String()
		req := &services.CreateHoldingRequest{
			Holding: &models.Holding{
				AssetId:     asset.Id,
				AccountId:   account.Id,
				PortfolioId: &randomUUID,
				Amount:      2,
				Decimals:    2,
			},
		}
		res, err := storageService.CreateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Empty(t, res)
	})
}

func TestGetHolding(t *testing.T) {
	storageService := getTransactionedService(t)

	asset := createTestAsset(t, storageService, "Test")
	user := createTestUser(t, storageService, "TestUser", "test@company.org")
	account := createTestAccount(t, storageService, user.Id, "Test broker account", models.AccountType_ACCOUNT_TYPE_BROKER)
	portfolio := createTestPortfolio(t, storageService, user.Id)
	holding := createTestHolding(t, storageService, asset.Id, account.Id, portfolio.Id)

	t.Run("valid holding retrieval", func(t *testing.T) {
		res, err := storageService.GetHolding(t.Context(), &services.GetHoldingRequest{Id: holding.Id})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "holding is not retrieved")
		}
		assert.Equal(t, holding.Id, res.Id)
		assert.Equal(t, holding.AssetId, res.AssetId)
		assert.Equal(t, holding.AccountId, res.AccountId)
		assert.Equal(t, holding.PortfolioId, res.PortfolioId)
		assert.Equal(t, holding.Amount, res.Amount)
		assert.Equal(t, holding.Decimals, res.Decimals)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)
	})

	t.Run("non-existing holding retrieval", func(t *testing.T) {
		res, err := storageService.GetHolding(t.Context(), &services.GetHoldingRequest{Id: "00000000-0000-0000-0000-000000000000"})
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("invalid holding ID", func(t *testing.T) {
		res, err := storageService.GetHolding(t.Context(), &services.GetHoldingRequest{Id: "not-a-uuid"})
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("empty ID", func(t *testing.T) {
		res, err := storageService.GetHolding(t.Context(), &services.GetHoldingRequest{Id: ""})
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Empty(t, res)
	})
}

func TestUpdateHolding(t *testing.T) {
	storageService := getTransactionedService(t)

	asset := createTestAsset(t, storageService, "Test")
	user := createTestUser(t, storageService, "TestUser", "test@company.org")
	account := createTestAccount(t, storageService, user.Id, "Test broker account", models.AccountType_ACCOUNT_TYPE_BROKER)
	portfolio := createTestPortfolio(t, storageService, user.Id)
	holding := createTestHolding(t, storageService, asset.Id, account.Id, portfolio.Id)

	t.Run("update amount and decimals", func(t *testing.T) {
		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:       holding.Id,
				Amount:   200,
				Decimals: 8,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"amount", "Decimals"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotEmpty(t, res) {
			assert.FailNow(t, "holding is not updated")
		}
		assert.Equal(t, req.Holding.Amount, res.Amount)
		assert.Equal(t, req.Holding.Decimals, res.Decimals)
		assert.Equal(t, holding.Id, res.Id)
		assert.Equal(t, holding.AssetId, res.AssetId)
		assert.Equal(t, holding.AccountId, res.AccountId)
		assert.Equal(t, holding.PortfolioId, res.PortfolioId)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)

		holding = res
	})

	t.Run("update asset_id", func(t *testing.T) {
		newAsset := createTestAsset(t, storageService, "New Asset")

		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:      holding.Id,
				AssetId: newAsset.Id,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"asset_id"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotEmpty(t, res) {
			assert.FailNow(t, "holding is not updated")
		}
		assert.Equal(t, req.Holding.AssetId, res.AssetId)
		assert.Equal(t, holding.Id, res.Id)
		assert.Equal(t, holding.AccountId, res.AccountId)
		assert.Equal(t, holding.PortfolioId, res.PortfolioId)
		assert.Equal(t, holding.Amount, res.Amount)
		assert.Equal(t, holding.Decimals, res.Decimals)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)

		holding = res
	})

	t.Run("update account_id", func(t *testing.T) {
		newAccount := createTestAccount(t, storageService, user.Id, "New Account", models.AccountType_ACCOUNT_TYPE_BROKER)
		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:        holding.Id,
				AccountId: newAccount.Id,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"account_id"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotEmpty(t, res) {
			assert.FailNow(t, "holding is not updated")
		}
		assert.Equal(t, req.Holding.AccountId, res.AccountId)
		assert.Equal(t, holding.Id, res.Id)
		assert.Equal(t, holding.AssetId, res.AssetId)
		assert.Equal(t, holding.PortfolioId, res.PortfolioId)
		assert.Equal(t, holding.Amount, res.Amount)
		assert.Equal(t, holding.Decimals, res.Decimals)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)

		holding = res
	})

	t.Run("update portfolio_id", func(t *testing.T) {
		newPortfolio := createTestPortfolio(t, storageService, user.Id)
		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:          holding.Id,
				PortfolioId: &newPortfolio.Id,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"portfolio_id"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotEmpty(t, res) {
			assert.FailNow(t, "holding is not updated")
		}
		assert.Equal(t, req.Holding.PortfolioId, res.PortfolioId)
		assert.Equal(t, holding.Id, res.Id)
		assert.Equal(t, holding.AssetId, res.AssetId)
		assert.Equal(t, holding.AccountId, res.AccountId)
		assert.Equal(t, holding.Amount, res.Amount)
		assert.Equal(t, holding.Decimals, res.Decimals)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)

		holding = res
	})

	t.Run("update non-existing holding", func(t *testing.T) {
		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:     uuid.New().String(),
				Amount: 10,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"amount"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("empty update mask", func(t *testing.T) {
		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:     holding.Id,
				Amount: 123,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("no holding", func(t *testing.T) {
		req := &services.UpdateHoldingRequest{
			Holding:    nil,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"amount"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Empty(t, res)
	})

	t.Run("invalid holding ID", func(t *testing.T) {
		req := &services.UpdateHoldingRequest{
			Holding: &models.Holding{
				Id:     "not-a-uuid",
				Amount: 10,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"amount"}},
		}
		res, err := storageService.UpdateHolding(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Empty(t, res)
	})
}

func TestListHoldings(t *testing.T) {
	storageService := getTransactionedService(t)

	asset := createTestAsset(t, storageService, "Test")
	user := createTestUser(t, storageService, "TestUser", "test@company.org")
	account1 := createTestAccount(t, storageService, user.Id, "Test broker account", models.AccountType_ACCOUNT_TYPE_BROKER)
	account2 := createTestAccount(t, storageService, user.Id, "Test broker account 2", models.AccountType_ACCOUNT_TYPE_BROKER)
	portfolio1 := createTestPortfolio(t, storageService, user.Id)
	portfolio2 := createTestPortfolio(t, storageService, user.Id)
	holding1 := createTestHolding(t, storageService, asset.Id, account1.Id, portfolio1.Id)
	holding2 := createTestHolding(t, storageService, asset.Id, account2.Id, portfolio1.Id)
	holding3 := createTestHolding(t, storageService, asset.Id, account1.Id, portfolio2.Id)

	t.Run("list all holdings", func(t *testing.T) {
		res, err := storageService.ListHoldings(t.Context(), &services.ListHoldingsRequest{})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "holdings are not listed")
		}
		assert.Equal(t, 3, len(res.Holdings))
		assert.Equal(t, holding1.Id, res.Holdings[0].Id)
		assert.Equal(t, holding2.Id, res.Holdings[1].Id)
		assert.Equal(t, holding3.Id, res.Holdings[2].Id)
	})

	t.Run("list holdings by account", func(t *testing.T) {
		res, err := storageService.ListHoldings(t.Context(), &services.ListHoldingsRequest{AccountId: &account1.Id})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "holdings are not listed")
		}
		assert.Equal(t, 2, len(res.Holdings))
		assert.Equal(t, holding1.Id, res.Holdings[0].Id)
		assert.Equal(t, holding3.Id, res.Holdings[1].Id)
	})

	t.Run("list holdings by portfolio", func(t *testing.T) {
		res, err := storageService.ListHoldings(t.Context(), &services.ListHoldingsRequest{PortfolioId: &portfolio2.Id})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "holdings are not listed")
		}
		assert.Equal(t, 1, len(res.Holdings))
		assert.Equal(t, holding3.Id, res.Holdings[0].Id)
	})

	t.Run("list holdings by asset", func(t *testing.T) {
		res, err := storageService.ListHoldings(t.Context(), &services.ListHoldingsRequest{AssetId: &asset.Id})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "holdings are not listed")
		}
		assert.Equal(t, 3, len(res.Holdings))
		assert.Equal(t, holding1.Id, res.Holdings[0].Id)
		assert.Equal(t, holding2.Id, res.Holdings[1].Id)
		assert.Equal(t, holding3.Id, res.Holdings[2].Id)
	})

	t.Run("list holdings with page size", func(t *testing.T) {
		pageSize := int32(1)
		res, err := storageService.ListHoldings(t.Context(), &services.ListHoldingsRequest{PageSize: &pageSize})
		if !assert.NoError(t, err) {
			assert.FailNow(t, "holdings are not listed")
		}
		assert.Equal(t, 1, len(res.Holdings))
		assert.Equal(t, holding1.Id, res.Holdings[0].Id)
		assert.NotEmpty(t, res.NextPageToken)
	})
}
