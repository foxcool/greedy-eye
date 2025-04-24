package storage

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/portfolio"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/user"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func createTestPortfolio(t *testing.T, s *StorageService, userID string) *models.Portfolio {
	portfolio, err := s.CreatePortfolio(t.Context(), &services.CreatePortfolioRequest{
		Portfolio: &models.Portfolio{
			UserId: userID,
			Name:   "Test Portfolio",
		},
	})
	if !assert.NoError(t, err) || !assert.NotNil(t, portfolio) {
		assert.FailNow(t, "can't create portfolio")
	}

	assert.NotEmpty(t, portfolio.Id)
	assert.NotEmpty(t, portfolio.CreatedAt)
	assert.Equal(t, userID, portfolio.UserId)
	assert.Equal(t, "Test Portfolio", portfolio.Name)

	return portfolio
}

func TestCreatePortfolio(t *testing.T) {
	storageService := getTransactionedService(t, user.Table, portfolio.Table)
	user := createTestUser(t, storageService, "John Doe", "john.doe@example.com")

	t.Run("Valid portfolio creation", func(t *testing.T) {
		req := &services.CreatePortfolioRequest{
			Portfolio: &models.Portfolio{
				UserId: user.Id,
				Name:   "TestPortfolio",
			},
		}
		res, err := storageService.CreatePortfolio(t.Context(), req)
		if !assert.NoError(t, err, "portfolio creation should succeed") || !assert.NotNil(t, res) {
			assert.FailNow(t, "portfolio creation failed")
		}
		assert.Equal(t, req.Portfolio.UserId, res.UserId)
		assert.Equal(t, req.Portfolio.Name, res.Name)
		assert.NotEmpty(t, res.Id)
		assert.NotEmpty(t, res.CreatedAt)
	})

	t.Run("Missing required fields (name)", func(t *testing.T) {
		req := &services.CreatePortfolioRequest{Portfolio: &models.Portfolio{
			UserId: user.Id,
		}}
		res, err := storageService.CreatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio creation should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Missing required fields (user_id)", func(t *testing.T) {
		req := &services.CreatePortfolioRequest{Portfolio: &models.Portfolio{
			Name: "NoUserPortfolio",
		}}
		res, err := storageService.CreatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio creation should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Invalid user_id format", func(t *testing.T) {
		req := &services.CreatePortfolioRequest{Portfolio: &models.Portfolio{
			UserId: "invalid-uuid",
			Name:   "InvalidUserPortfolio",
		}}
		res, err := storageService.CreatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio creation should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Non-existent user_id", func(t *testing.T) {
		req := &services.CreatePortfolioRequest{Portfolio: &models.Portfolio{
			UserId: uuid.New().String(),
			Name:   "NonExistentUserPortfolio",
		}}
		res, err := storageService.CreatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio creation should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestGetPortfolio(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "Alice", "alice@mail.com")
	portfolio := createTestPortfolio(t, storageService, user.Id)

	t.Run("Valid portfolio retrieval", func(t *testing.T) {
		got, err := storageService.GetPortfolio(t.Context(), &services.GetPortfolioRequest{Id: portfolio.Id})
		if !assert.NoError(t, err) || !assert.NotNil(t, got) {
			assert.FailNow(t, "failed to get portfolio")
		}
		assert.Equal(t, portfolio.Id, got.Id)
		assert.Equal(t, portfolio.Name, got.Name)
		assert.Equal(t, portfolio.UserId, got.UserId)
		assert.Equal(t, portfolio.CreatedAt, got.CreatedAt)
		assert.Equal(t, portfolio.UpdatedAt, got.UpdatedAt)
	})

	t.Run("Non-existent portfolio retrieval", func(t *testing.T) {
		res, err := storageService.GetPortfolio(t.Context(), &services.GetPortfolioRequest{Id: uuid.New().String()})
		assert.Error(t, err, "portfolio retrieval should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Invalid portfolio ID format", func(t *testing.T) {
		res, err := storageService.GetPortfolio(t.Context(), &services.GetPortfolioRequest{Id: "invalid-uuid"})
		assert.Error(t, err, "portfolio retrieval should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Empty portfolio ID", func(t *testing.T) {
		res, err := storageService.GetPortfolio(t.Context(), &services.GetPortfolioRequest{})
		assert.Error(t, err, "portfolio retrieval should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestUpdatePortfolio(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "Bob", "bob@testmail.earth")
	portfolio := createTestPortfolio(t, storageService, user.Id)
	description := "Updated description"

	t.Run("Valid portfolio update", func(t *testing.T) {
		req := &services.UpdatePortfolioRequest{
			Portfolio: &models.Portfolio{
				Id:          portfolio.Id,
				Name:        "Updated Portfolio",
				Description: &description,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "description"}},
		}
		res, err := storageService.UpdatePortfolio(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, res) {
			assert.FailNow(t, "portfolio update failed")
		}
		assert.Equal(t, req.Portfolio.Id, res.Id)
		assert.Equal(t, portfolio.CreatedAt, res.CreatedAt)
		assert.NotEqual(t, portfolio.UpdatedAt, res.UpdatedAt)
		assert.Equal(t, req.Portfolio.Name, res.Name)
		assert.Equal(t, portfolio.UserId, res.UserId)
		assert.Equal(t, description, *res.Description)
	})

	t.Run("Invalid portfolio ID format", func(t *testing.T) {
		req := &services.UpdatePortfolioRequest{
			Portfolio: &models.Portfolio{
				Id:   "invalid-uuid",
				Name: "Invalid Portfolio",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio update should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Empty portfolio ID", func(t *testing.T) {
		req := &services.UpdatePortfolioRequest{
			Portfolio: &models.Portfolio{
				Name: "Empty ID Portfolio",
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio update should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Missing update mask", func(t *testing.T) {
		req := &services.UpdatePortfolioRequest{
			Portfolio: &models.Portfolio{
				Id:   portfolio.Id,
				Name: "No Update Mask",
			},
		}
		res, err := storageService.UpdatePortfolio(t.Context(), req)
		assert.Error(t, err, "portfolio update should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestDeletePortfolio(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "Charlie", "charlie@job.earth")
	portfolio := createTestPortfolio(t, storageService, user.Id)

	t.Run("Valid portfolio deletion", func(t *testing.T) {
		res, err := storageService.DeletePortfolio(t.Context(), &services.DeletePortfolioRequest{Id: portfolio.Id})
		assert.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("Non-existent portfolio deletion", func(t *testing.T) {
		res, err := storageService.DeletePortfolio(t.Context(), &services.DeletePortfolioRequest{Id: uuid.New().String()})
		assert.Error(t, err, "portfolio deletion should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Invalid portfolio ID format", func(t *testing.T) {
		res, err := storageService.DeletePortfolio(t.Context(), &services.DeletePortfolioRequest{Id: "invalid-uuid"})
		assert.Error(t, err, "portfolio deletion should fail")
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestListPortfolios(t *testing.T) {
	storageService := getTransactionedService(t)
	user0 := createTestUser(t, storageService, "TestUser1", "user1@mail@earth")
	user1 := createTestUser(t, storageService, "TestUser2", "user2@mail@earth")
	portfolio0 := createTestPortfolio(t, storageService, user0.Id)
	portfolio1 := createTestPortfolio(t, storageService, user0.Id)
	portfolio2 := createTestPortfolio(t, storageService, user1.Id)

	t.Run("List portfolios", func(t *testing.T) {
		res, err := storageService.ListPortfolios(t.Context(), &services.ListPortfoliosRequest{})
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.NotEmpty(t, res.Portfolios)
		assert.Greater(t, len(res.Portfolios), 0)
	})

	t.Run("List portfolios by user 1", func(t *testing.T) {
		res, err := storageService.ListPortfolios(t.Context(), &services.ListPortfoliosRequest{
			UserId: &user0.Id,
		})
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, 2, len(res.Portfolios))
		assert.Equal(t, portfolio0.Id, res.Portfolios[0].Id)
		assert.Equal(t, portfolio1.Id, res.Portfolios[1].Id)
	})

	t.Run("List portfolios by user 2", func(t *testing.T) {
		res, err := storageService.ListPortfolios(t.Context(), &services.ListPortfoliosRequest{
			UserId: &user1.Id,
		})
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, 1, len(res.Portfolios))
		assert.Equal(t, portfolio2.Id, res.Portfolios[0].Id)
	})

	t.Run("List portfolios with pagination", func(t *testing.T) {
		pageSize := int32(2)
		res, err := storageService.ListPortfolios(t.Context(), &services.ListPortfoliosRequest{
			PageSize: &pageSize,
		})
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, 2, len(res.Portfolios))
		assert.NotEmpty(t, res.NextPageToken)
	})
}
