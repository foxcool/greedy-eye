package storage

import (
	"encoding/base64"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func createTestPrice(t *testing.T, storageService *StorageService, assetId, baseAssetId, sourceId string) *models.Price {
	req := &services.CreatePriceRequest{
		Price: &models.Price{
			SourceId:    sourceId,
			AssetId:     assetId,
			BaseAssetId: baseAssetId,
			Interval:    "latest",
			Decimals:    4,
			Last:        rand.Int64N(1000000),
			Timestamp:   timestamppb.Now(),
		},
	}
	open := int64(rand.IntN(1000000))
	close := int64(rand.IntN(1000000))
	high := int64(rand.IntN(1000000))
	low := int64(rand.IntN(1000000))
	volume := int64(rand.IntN(1000000))
	req.Price.Open = &open
	req.Price.Close = &close
	req.Price.High = &high
	req.Price.Low = &low
	req.Price.Volume = &volume

	price, err := storageService.CreatePrice(t.Context(), req)
	assert.NoError(t, err)
	assert.NotEmpty(t, price.Id)
	assert.Equal(t, req.Price.SourceId, price.SourceId)
	assert.Equal(t, req.Price.AssetId, price.AssetId)
	assert.Equal(t, req.Price.BaseAssetId, price.BaseAssetId)
	assert.Equal(t, req.Price.Interval, price.Interval)
	assert.Equal(t, req.Price.Decimals, price.Decimals)
	assert.Equal(t, req.Price.Last, price.Last)
	assert.Equal(t, req.Price.Open, price.Open)
	assert.Equal(t, req.Price.Close, price.Close)
	assert.Equal(t, req.Price.High, price.High)
	assert.Equal(t, req.Price.Low, price.Low)
	assert.Equal(t, req.Price.Volume, price.Volume)

	return price
}

func TestCreatePrice(t *testing.T) {
	storageService := getTransactionedService(t)
	asset1 := createTestAsset(t, storageService, "TestToken1")
	asset2 := createTestAsset(t, storageService, "TestToken2")

	t.Run("CreatePrice", func(t *testing.T) {
		req := &services.CreatePriceRequest{
			Price: &models.Price{
				SourceId:    "tracker",
				AssetId:     asset1.Id,
				BaseAssetId: asset2.Id,
				Interval:    "1m",
				Last:        1000000,
				Decimals:    2,
				Timestamp:   timestamppb.Now(),
			},
		}
		price, err := storageService.CreatePrice(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, price) {
			t.Fail()
		}
		assert.NotEmpty(t, price.Id)
		assert.NotEmpty(t, price.Timestamp)
		assert.Equal(t, req.Price.SourceId, price.SourceId)
		assert.Equal(t, req.Price.AssetId, price.AssetId)
		assert.Equal(t, req.Price.BaseAssetId, price.BaseAssetId)
		assert.Equal(t, req.Price.Interval, price.Interval)
		assert.Equal(t, req.Price.Decimals, price.Decimals)
		assert.Equal(t, req.Price.Last, price.Last)
		assert.Equal(t, req.Price.Open, price.Open)
		assert.Equal(t, req.Price.Close, price.Close)
		assert.Equal(t, req.Price.High, price.High)
		assert.Equal(t, req.Price.Low, price.Low)
		assert.Equal(t, req.Price.Volume, price.Volume)
	})

	t.Run("No Asset ID", func(t *testing.T) {
		req := &services.CreatePriceRequest{
			Price: &models.Price{
				SourceId:    "tracker",
				BaseAssetId: asset2.Id,
				Interval:    "1m",
				Last:        1000000,
				Decimals:    2,
				Timestamp:   timestamppb.Now(),
			},
		}
		res, err := storageService.CreatePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("No Base Asset ID", func(t *testing.T) {
		req := &services.CreatePriceRequest{
			Price: &models.Price{
				SourceId:  asset1.Id,
				AssetId:   asset2.Id,
				Interval:  "1m",
				Last:      1000000,
				Decimals:  2,
				Timestamp: timestamppb.Now(),
			},
		}
		res, err := storageService.CreatePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("No Source ID", func(t *testing.T) {
		req := &services.CreatePriceRequest{
			Price: &models.Price{
				AssetId:     asset1.Id,
				BaseAssetId: asset2.Id,
				Interval:    "1m",
				Last:        1000000,
				Decimals:    2,
				Timestamp:   timestamppb.Now(),
			},
		}
		res, err := storageService.CreatePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Not Found Asset ID", func(t *testing.T) {
		req := &services.CreatePriceRequest{
			Price: &models.Price{
				AssetId:     uuid.New().String(),
				BaseAssetId: asset2.Id,
				SourceId:    "binance",
				Last:        10,
				Interval:    "1m",
			},
		}
		_, err := storageService.CreatePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Not Found Base Asset ID", func(t *testing.T) {
		req := &services.CreatePriceRequest{
			Price: &models.Price{
				AssetId:     asset1.Id,
				BaseAssetId: uuid.New().String(),
				SourceId:    "binance",
				Last:        10,
				Interval:    "1m",
			},
		}
		_, err := storageService.CreatePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestGetLatestPrice(t *testing.T) {
	storageService := getTransactionedService(t)
	asset1 := createTestAsset(t, storageService, "TestToken1")
	asset2 := createTestAsset(t, storageService, "TestToken2")
	createTestPrice(t, storageService, asset1.Id, asset2.Id, "exchange1")
	price2 := createTestPrice(t, storageService, asset1.Id, asset2.Id, "exchange2")
	price3 := createTestPrice(t, storageService, asset1.Id, asset2.Id, "aggregator")

	t.Run("Get latest price", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     asset1.Id,
			BaseAssetId: asset2.Id,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, price) {
			t.Fail()
		}
		assert.Equal(t, price3.Id, price.Id)
		assert.Equal(t, price3.SourceId, price.SourceId)
		assert.Equal(t, price3.AssetId, price.AssetId)
		assert.Equal(t, price3.BaseAssetId, price.BaseAssetId)
		assert.Equal(t, price3.Interval, price.Interval)
		assert.Equal(t, price3.Decimals, price.Decimals)
		assert.Equal(t, price3.Last, price.Last)
		assert.Equal(t, price3.Open, price.Open)
		assert.Equal(t, price3.Close, price.Close)
		assert.Equal(t, price3.High, price.High)
		assert.Equal(t, price3.Low, price.Low)
		assert.Equal(t, price3.Volume, price.Volume)
	})

	t.Run("Get latest price with source", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     asset1.Id,
			BaseAssetId: asset2.Id,
			SourceId:    &price2.SourceId,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		if !assert.NoError(t, err) || !assert.NotNil(t, price) {
			t.Fail()
		}
		assert.Equal(t, price2.Id, price.Id)
		assert.Equal(t, price2.SourceId, price.SourceId)
		assert.Equal(t, price2.AssetId, price.AssetId)
		assert.Equal(t, price2.BaseAssetId, price.BaseAssetId)
		assert.Equal(t, price2.Interval, price.Interval)
		assert.Equal(t, price2.Decimals, price.Decimals)
		assert.Equal(t, price2.Last, price.Last)
		assert.Equal(t, price2.Open, price.Open)
		assert.Equal(t, price2.Close, price.Close)
		assert.Equal(t, price2.High, price.High)
		assert.Equal(t, price2.Low, price.Low)
		assert.Equal(t, price2.Volume, price.Volume)
	})

	t.Run("Get latest price with non-existing source", func(t *testing.T) {
		unknown := "unknown"
		req := &services.GetLatestPriceRequest{
			AssetId:     asset1.Id,
			BaseAssetId: asset2.Id,
			SourceId:    &unknown,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Get latest price with non-existing asset", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     uuid.New().String(),
			BaseAssetId: asset2.Id,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Get latest price with non-existing base asset", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     asset1.Id,
			BaseAssetId: uuid.New().String(),
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Get latest price with empty asset ID", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     "",
			BaseAssetId: asset2.Id,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Get latest price with empty base asset ID", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     asset1.Id,
			BaseAssetId: "",
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestListPriceHistory(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "TestAsset")
	baseAsset := createTestAsset(t, storageService, "TestBaseAsset")
	prices := make([]*models.Price, 0)
	for i := 0; i <= 5; i++ {
		price := createTestPrice(t, storageService, asset.Id, baseAsset.Id, "exchange")
		prices = append(prices, price)
	}
	var cursor string

	t.Run("List price history in range", func(t *testing.T) {
		from := timestamppb.New(time.Now().Add(-time.Minute * 5))
		to := timestamppb.New(time.Now())
		req := &services.ListPriceHistoryRequest{
			AssetId:     asset.Id,
			BaseAssetId: baseAsset.Id,
			From:        from,
			To:          to,
			PageSize:    nil,
		}
		res, err := storageService.ListPriceHistory(t.Context(), req)
		if !assert.NoError(t, err) {
			t.Fail()
		}
		assert.GreaterOrEqual(t, len(res.Prices), 4)
	})

	t.Run("List price history with non-existing asset", func(t *testing.T) {
		req := &services.ListPriceHistoryRequest{
			AssetId:     uuid.New().String(),
			BaseAssetId: baseAsset.Id,
		}
		res, err := storageService.ListPriceHistory(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Pagination with page size 2", func(t *testing.T) {
		pageSize := int32(2)
		req := &services.ListPriceHistoryRequest{
			AssetId:     asset.Id,
			BaseAssetId: baseAsset.Id,
			PageSize:    &pageSize,
		}
		res, err := storageService.ListPriceHistory(t.Context(), req)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to list price history")
		}
		assert.Len(t, res.Prices, 2)
		assert.Equal(t, prices[0].Id, res.Prices[0].Id)
		assert.Equal(t, prices[1].Id, res.Prices[1].Id)
		if assert.NotEmpty(t, res.NextPageToken) {
			cursor = res.NextPageToken
		}
	})

	t.Run("Pagination with page token", func(t *testing.T) {
		if cursor == "" {
			t.Log("Cursor is empty, using second price timestamp as cursor")
			txt, _ := prices[1].Timestamp.AsTime().MarshalText()
			cursor = base64.StdEncoding.EncodeToString(txt)
		}

		pageSize := int32(2)
		req := &services.ListPriceHistoryRequest{
			AssetId:     asset.Id,
			BaseAssetId: baseAsset.Id,
			PageSize:    &pageSize,
			PageToken:   &cursor,
		}
		res, err := storageService.ListPriceHistory(t.Context(), req)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to list price history")
		}
		assert.Len(t, res.Prices, 2)
		assert.Equal(t, prices[2].Id, res.Prices[0].Id)
		assert.Equal(t, prices[3].Id, res.Prices[1].Id)
		assert.NotEmpty(t, res.NextPageToken)
	})
}

func TestDeletePrice(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "TestToken1")
	baseAsset := createTestAsset(t, storageService, "TestToken2")
	price := createTestPrice(t, storageService, asset.Id, baseAsset.Id, "exchange")
	t.Run("Delete price", func(t *testing.T) {
		req := &services.DeletePriceRequest{
			Id: price.Id,
		}
		res, err := storageService.DeletePrice(t.Context(), req)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to delete price")
		}
		assert.NotNil(t, res)
	})

	t.Run("Get deleted price", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     asset.Id,
			BaseAssetId: baseAsset.Id,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Delete non-existing price", func(t *testing.T) {
		req := &services.DeletePriceRequest{
			Id: uuid.New().String(),
		}
		res, err := storageService.DeletePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Delete price with empty ID", func(t *testing.T) {
		req := &services.DeletePriceRequest{
			Id: "",
		}
		res, err := storageService.DeletePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("Delete price with invalid ID", func(t *testing.T) {
		req := &services.DeletePriceRequest{
			Id: "invalid-id",
		}
		res, err := storageService.DeletePrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestDeletePrices(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "TestToken1")
	baseAsset := createTestAsset(t, storageService, "TestToken2")
	for range 3 {
		createTestPrice(t, storageService, asset.Id, baseAsset.Id, "exchange")
	}

	t.Run("Delete prices", func(t *testing.T) {
		req := &services.DeletePricesRequest{
			AssetId:     &asset.Id,
			BaseAssetId: &baseAsset.Id,
		}
		res, err := storageService.DeletePrices(t.Context(), req)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to delete prices")
		}
		assert.NotNil(t, res)
	})

	t.Run("Get deleted prices", func(t *testing.T) {
		req := &services.GetLatestPriceRequest{
			AssetId:     asset.Id,
			BaseAssetId: baseAsset.Id,
		}
		price, err := storageService.GetLatestPrice(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Delete non-existing prices", func(t *testing.T) {
		notExistingAssetID := uuid.New().String()
		req := &services.DeletePricesRequest{
			AssetId:     &notExistingAssetID,
			BaseAssetId: &baseAsset.Id,
		}
		res, err := storageService.DeletePrices(t.Context(), req)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}
