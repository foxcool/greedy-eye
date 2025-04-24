package storage

import (
	"regexp"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func createTestAsset(t *testing.T, storageService *StorageService, name string) *models.Asset {
	// replace all sorts of spaces to nothing by \s regexp
	symbol := strings.ToUpper(regexp.MustCompile(`\s`).ReplaceAllString(name, ""))
	req := &services.CreateAssetRequest{
		Asset: &models.Asset{
			Symbol: &symbol,
			Name:   name,
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags: []string{
				name,
				"test",
			},
		},
	}
	asset, err := storageService.CreateAsset(t.Context(), req)
	if !assert.NoError(t, err, "asset creation failed") || !assert.NotNil(t, asset) {
		assert.FailNow(t, "asset creation failed")
	}
	assert.Equal(t, req.Asset.Symbol, asset.Symbol)
	assert.Equal(t, req.Asset.Name, asset.Name)
	assert.Equal(t, req.Asset.Type, asset.Type)
	assert.ElementsMatch(t, req.Asset.Tags, asset.Tags)
	assert.NotEmpty(t, asset.Id)
	assert.NotEmpty(t, asset.CreatedAt)

	return asset
}

func TestCreateAsset(t *testing.T) {
	storageService := getTransactionedService(t)

	t.Run("Valid asset creation", func(t *testing.T) {
		symbol := "randSYM"
		req := &services.CreateAssetRequest{
			Asset: &models.Asset{
				Symbol: &symbol,
				Name:   "MegaAsset",
				Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
				Tags: []string{
					"megaasset",
					"pos",
				},
			},
		}
		asset, err := storageService.CreateAsset(t.Context(), req)
		if !assert.NoError(t, err, "asset creation failed") || !assert.NotNil(t, asset) {
			assert.FailNow(t, "asset creation failed")
		}
		assert.Equal(t, req.Asset.Symbol, asset.Symbol)
		assert.Equal(t, req.Asset.Name, asset.Name)
		assert.Equal(t, req.Asset.Type, asset.Type)
		assert.ElementsMatch(t, req.Asset.Tags, asset.Tags)
	})

	t.Run("Missing required fields (name)", func(t *testing.T) {
		symbol := "NoNaMeAsset"
		req := &services.CreateAssetRequest{
			Asset: &models.Asset{
				Symbol: &symbol,
				Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
				Tags: []string{
					"NNM",
					"love",
				},
			},
		}
		asset, err := storageService.CreateAsset(t.Context(), req)
		if assert.Error(t, err, "asset creation should fail") {
			assert.Nil(t, asset)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		}
	})

	t.Run("Missing required fields (type)", func(t *testing.T) {
		symbol := "UnTyPeD"
		req := &services.CreateAssetRequest{
			Asset: &models.Asset{
				Symbol: &symbol,
				Name:   "UnTyPeD",
				Tags: []string{
					"what",
				},
			},
		}
		asset, err := storageService.CreateAsset(t.Context(), req)
		if assert.Error(t, err, "asset creation should fail") {
			assert.Nil(t, asset)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		}
	})

	t.Run("nil asset in request", func(t *testing.T) {
		req := &services.CreateAssetRequest{
			Asset: nil,
		}
		asset, err := storageService.CreateAsset(t.Context(), req)
		if assert.Error(t, err, "asset creation should fail") {
			assert.Nil(t, asset)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		}
	})
}

func TestGetAsset(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "Test")

	t.Run("Get existing asset by ID", func(t *testing.T) {
		req := &services.GetAssetRequest{Id: asset.Id}
		res, err := storageService.GetAsset(t.Context(), req)
		if assert.NoError(t, err) {
			assert.Equal(t, asset.Id, res.Id)
			assert.Equal(t, asset.Symbol, res.Symbol)
			assert.Equal(t, asset.Name, res.Name)
			assert.Equal(t, asset.Type, res.Type)
			assert.Equal(t, asset.Tags, res.Tags)
			assert.NotEmpty(t, res.CreatedAt)
		}
	})

	t.Run("Get non-existent asset by ID", func(t *testing.T) {
		res, err := storageService.GetAsset(t.Context(), &services.GetAssetRequest{Id: uuid.New().String()})
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Invalid asset ID", func(t *testing.T) {
		res, err := storageService.GetAsset(t.Context(), &services.GetAssetRequest{Id: "not-a-uuid"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})

	t.Run("Empty asset ID", func(t *testing.T) {
		res, err := storageService.GetAsset(t.Context(), &services.GetAssetRequest{Id: ""})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Nil(t, res)
	})
}

func TestUpdateAsset(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "Test")

	t.Run("Update asset name and tag", func(t *testing.T) {
		req := &services.UpdateAssetRequest{
			Asset: &models.Asset{
				Id:   asset.Id,
				Name: "New Name",
				Tags: []string{"crypto", "pos"},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "tags"}},
		}
		res, err := storageService.UpdateAsset(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, res) {
			assert.FailNow(t, "Unexpected error or nil response")
		}
		assert.Equal(t, req.Asset.Id, res.Id)
		assert.Equal(t, req.Asset.Name, res.Name)
		assert.ElementsMatch(t, req.Asset.Tags, res.Tags)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.Equal(t, asset.Type, res.Type)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)

		asset = res
	})

	t.Run("Update type", func(t *testing.T) {
		req := &services.UpdateAssetRequest{
			Asset: &models.Asset{
				Id:   asset.Id,
				Type: models.AssetType_ASSET_TYPE_STOCK,
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"type"}},
		}
		res, err := storageService.UpdateAsset(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, res) {
			assert.FailNow(t, "Unexpected error or nil response")
		}
		assert.Equal(t, req.Asset.Id, res.Id)
		assert.Equal(t, req.Asset.Type, res.Type)
		assert.Equal(t, asset.Name, res.Name)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.ElementsMatch(t, asset.Tags, res.Tags)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)

		asset = res
	})

	t.Run("Update with unknown field in update mask (should warn, not fail)", func(t *testing.T) {
		req := &services.UpdateAssetRequest{
			Asset:      &models.Asset{Id: asset.Id},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"nonexistent_field"}},
		}
		res, err := storageService.UpdateAsset(t.Context(), req)
		// Should not be an error, just a warning log
		if !assert.NoError(t, err) || !assert.NotNil(t, res) {
			assert.FailNow(t, "Unexpected error or nil response")
		}
		assert.Equal(t, asset.Id, res.Id)
		assert.Equal(t, asset.Type, res.Type)
		assert.Equal(t, asset.Name, res.Name)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.ElementsMatch(t, asset.Tags, res.Tags)
		assert.NotEmpty(t, res.CreatedAt)
		assert.NotEmpty(t, res.UpdatedAt)
	})

	t.Run("Update non-existent asset", func(t *testing.T) {
		req := &services.UpdateAssetRequest{
			Asset:      &models.Asset{Id: uuid.New().String(), Name: "Doesn't Exist"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}
		res, err := storageService.UpdateAsset(t.Context(), req)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Nil(t, res)
	})
}

func TestDeleteAsset(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "Test")

	t.Run("Delete existing asset", func(t *testing.T) {
		_, err := storageService.DeleteAsset(t.Context(), &services.DeleteAssetRequest{Id: asset.Id})
		assert.NoError(t, err)
		// Try to get after delete
		res, err := storageService.GetAsset(t.Context(), &services.GetAssetRequest{Id: asset.Id})
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Delete non-existent asset", func(t *testing.T) {
		_, err := storageService.DeleteAsset(t.Context(), &services.DeleteAssetRequest{Id: uuid.New().String()})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Invalid asset ID", func(t *testing.T) {
		_, err := storageService.DeleteAsset(t.Context(), &services.DeleteAssetRequest{Id: "not-a-uuid"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestListAssets(t *testing.T) {
	storageService := getTransactionedService(t)
	assets := make(map[string]*models.Asset)
	for _, assetName := range []string{"asset1", "asset2", "asset3"} {
		assets[assetName] = createTestAsset(t, storageService, assetName)
	}

	t.Run("List all", func(t *testing.T) {
		resp, err := storageService.ListAssets(t.Context(), &services.ListAssetsRequest{})
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, len(resp.Assets), 3)
		for _, actual := range resp.Assets {
			if expected, ok := assets[actual.Name]; ok {
				assert.Equal(t, expected.Id, actual.Id)
				assert.Equal(t, expected.Name, actual.Name)
				assert.Equal(t, expected.Type, actual.Type)
				assert.Equal(t, expected.Symbol, actual.Symbol)
				assert.ElementsMatch(t, expected.Tags, actual.Tags)
				assert.NotEmpty(t, actual.CreatedAt)
				assert.NotEmpty(t, actual.UpdatedAt)
			}
		}
	})

	t.Run("Filter by asset2 tag (only 1 asset)", func(t *testing.T) {
		res, err := storageService.ListAssets(t.Context(), &services.ListAssetsRequest{Tags: []string{"asset2"}})
		if !assert.NoError(t, err) || !assert.Len(t, res.Assets, 1) {
			assert.FailNow(t, "Should return 1 asset")
		}
		for _, a := range res.Assets {
			assert.Contains(t, a.Tags, "asset2")
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		pageSize := int32(2)
		resp, err := storageService.ListAssets(t.Context(), &services.ListAssetsRequest{PageSize: &pageSize})
		if !assert.NoError(t, err) || !assert.Len(t, resp.Assets, 2) || resp.NextPageToken == "" {
			assert.FailNow(t, "Should return 2 assets and next page token")
		}
		next, err := storageService.ListAssets(t.Context(), &services.ListAssetsRequest{PageToken: &resp.NextPageToken})
		assert.NoError(t, err)
		assert.NotEmpty(t, next.Assets)
	})
}
