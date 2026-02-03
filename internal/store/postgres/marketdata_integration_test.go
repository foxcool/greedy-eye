//go:build integration

package postgres

import (
	"context"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestAsset(t *testing.T, s *MarketDataStore, name string) *entity.Asset {
	t.Helper()
	symbol := strings.ToUpper(regexp.MustCompile(`\s`).ReplaceAllString(name, ""))
	asset := &entity.Asset{
		Symbol: symbol,
		Name:   name,
		Type:   entity.AssetTypeCryptocurrency,
		Tags:   []string{name, "test"},
	}
	created, err := s.CreateAsset(context.Background(), asset)
	require.NoError(t, err, "asset creation failed")
	require.NotNil(t, created)
	assert.Equal(t, asset.Symbol, created.Symbol)
	assert.Equal(t, asset.Name, created.Name)
	assert.Equal(t, asset.Type, created.Type)
	assert.ElementsMatch(t, asset.Tags, created.Tags)
	assert.NotEmpty(t, created.ID)
	assert.NotZero(t, created.CreatedAt)

	return created
}

func createTestPrice(t *testing.T, s *MarketDataStore, assetID, baseAssetID, sourceID string) *entity.StoredPrice {
	t.Helper()
	open := rand.Int64N(1000000)
	close := rand.Int64N(1000000)
	high := rand.Int64N(1000000)
	low := rand.Int64N(1000000)
	volume := rand.Int64N(1000000)

	price := &entity.StoredPrice{
		SourceID:    sourceID,
		AssetID:     assetID,
		BaseAssetID: baseAssetID,
		Interval:    "latest",
		Decimals:    4,
		Last:        rand.Int64N(1000000),
		Open:        &open,
		Close:       &close,
		High:        &high,
		Low:         &low,
		Volume:      &volume,
		Timestamp:   time.Now(),
	}

	created, err := s.CreatePrice(context.Background(), price)
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, price.SourceID, created.SourceID)
	assert.Equal(t, price.AssetID, created.AssetID)
	assert.Equal(t, price.BaseAssetID, created.BaseAssetID)

	return created
}

func TestCreateAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)

	t.Run("Valid asset creation", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "BTC",
			Name:   "Bitcoin",
			Type:   entity.AssetTypeCryptocurrency,
			Tags:   []string{"crypto", "pow"},
		}
		created, err := s.CreateAsset(context.Background(), asset)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, asset.Symbol, created.Symbol)
		assert.Equal(t, asset.Name, created.Name)
		assert.Equal(t, asset.Type, created.Type)
		assert.ElementsMatch(t, asset.Tags, created.Tags)
	})

	t.Run("Missing required fields (name)", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "NONAME",
			Type:   entity.AssetTypeCryptocurrency,
		}
		_, err := s.CreateAsset(context.Background(), asset)
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Missing required fields (type)", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "NOTYPE",
			Name:   "No Type Asset",
		}
		_, err := s.CreateAsset(context.Background(), asset)
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("nil asset", func(t *testing.T) {
		_, err := s.CreateAsset(context.Background(), nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestGetAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "TestGetAsset")

	t.Run("Get existing asset by ID", func(t *testing.T) {
		res, err := s.GetAsset(context.Background(), asset.ID)
		require.NoError(t, err)
		assert.Equal(t, asset.ID, res.ID)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.Equal(t, asset.Name, res.Name)
		assert.Equal(t, asset.Type, res.Type)
		assert.ElementsMatch(t, asset.Tags, res.Tags)
	})

	t.Run("Get non-existent asset by ID", func(t *testing.T) {
		_, err := s.GetAsset(context.Background(), uuid.New().String())
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Invalid asset ID", func(t *testing.T) {
		_, err := s.GetAsset(context.Background(), "not-a-uuid")
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Empty asset ID", func(t *testing.T) {
		_, err := s.GetAsset(context.Background(), "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestUpdateAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "TestUpdateAsset")

	t.Run("Update asset name and tags", func(t *testing.T) {
		updated := &entity.Asset{
			ID:   asset.ID,
			Name: "New Name",
			Tags: []string{"crypto", "pos"},
		}
		res, err := s.UpdateAsset(context.Background(), updated, []string{"name", "tags"})
		require.NoError(t, err)
		assert.Equal(t, asset.ID, res.ID)
		assert.Equal(t, "New Name", res.Name)
		assert.ElementsMatch(t, []string{"crypto", "pos"}, res.Tags)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.Equal(t, asset.Type, res.Type)
	})

	t.Run("Update type", func(t *testing.T) {
		updated := &entity.Asset{
			ID:   asset.ID,
			Type: entity.AssetTypeStock,
		}
		res, err := s.UpdateAsset(context.Background(), updated, []string{"type"})
		require.NoError(t, err)
		assert.Equal(t, entity.AssetTypeStock, res.Type)
	})

	t.Run("Update non-existent asset", func(t *testing.T) {
		updated := &entity.Asset{
			ID:   uuid.New().String(),
			Name: "Doesn't Exist",
		}
		_, err := s.UpdateAsset(context.Background(), updated, []string{"name"})
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestDeleteAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "TestDeleteAsset")

	t.Run("Delete existing asset", func(t *testing.T) {
		err := s.DeleteAsset(context.Background(), asset.ID)
		assert.NoError(t, err)
		// Verify deletion
		_, err = s.GetAsset(context.Background(), asset.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existent asset", func(t *testing.T) {
		err := s.DeleteAsset(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Invalid asset ID", func(t *testing.T) {
		err := s.DeleteAsset(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestListAssets(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)

	// Create test assets
	assets := make(map[string]*entity.Asset)
	for _, name := range []string{"ListAsset1", "ListAsset2", "ListAsset3"} {
		assets[name] = createTestAsset(t, s, name)
	}

	t.Run("List all", func(t *testing.T) {
		res, _, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 3)
	})

	t.Run("Filter by tag", func(t *testing.T) {
		res, _, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{
			Tags: []string{"ListAsset2"},
		})
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Contains(t, res[0].Tags, "ListAsset2")
	})

	t.Run("Pagination", func(t *testing.T) {
		res, nextToken, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{
			PageSize: 2,
		})
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.NotEmpty(t, nextToken)

		// Get next page
		res2, _, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{
			PageSize:  2,
			PageToken: nextToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res2)
	})
}

func TestCreatePrice(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset1 := createTestAsset(t, s, "PriceAsset1")
	asset2 := createTestAsset(t, s, "PriceAsset2")

	t.Run("Create price", func(t *testing.T) {
		price := &entity.StoredPrice{
			SourceID:    "binance",
			AssetID:     asset1.ID,
			BaseAssetID: asset2.ID,
			Interval:    "1m",
			Last:        1000000,
			Decimals:    2,
			Timestamp:   time.Now(),
		}
		created, err := s.CreatePrice(context.Background(), price)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, price.SourceID, created.SourceID)
	})

	t.Run("Missing asset_id", func(t *testing.T) {
		price := &entity.StoredPrice{
			SourceID:    "binance",
			BaseAssetID: asset2.ID,
			Interval:    "1m",
			Last:        1000000,
		}
		_, err := s.CreatePrice(context.Background(), price)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Non-existent asset_id", func(t *testing.T) {
		price := &entity.StoredPrice{
			SourceID:    "binance",
			AssetID:     uuid.New().String(),
			BaseAssetID: asset2.ID,
			Interval:    "1m",
			Last:        1000000,
		}
		_, err := s.CreatePrice(context.Background(), price)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestGetLatestPrice(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset1 := createTestAsset(t, s, "LatestPriceAsset1")
	asset2 := createTestAsset(t, s, "LatestPriceAsset2")

	createTestPrice(t, s, asset1.ID, asset2.ID, "exchange1")
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	price2 := createTestPrice(t, s, asset1.ID, asset2.ID, "exchange2")
	time.Sleep(10 * time.Millisecond)
	price3 := createTestPrice(t, s, asset1.ID, asset2.ID, "aggregator")

	t.Run("Get latest price (any source)", func(t *testing.T) {
		res, err := s.GetLatestPrice(context.Background(), asset1.ID, asset2.ID, "")
		require.NoError(t, err)
		assert.Equal(t, price3.ID, res.ID)
	})

	t.Run("Get latest price with specific source", func(t *testing.T) {
		res, err := s.GetLatestPrice(context.Background(), asset1.ID, asset2.ID, "exchange2")
		require.NoError(t, err)
		assert.Equal(t, price2.ID, res.ID)
	})

	t.Run("Get latest price with non-existing source", func(t *testing.T) {
		_, err := s.GetLatestPrice(context.Background(), asset1.ID, asset2.ID, "unknown")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Get latest price with non-existing asset", func(t *testing.T) {
		_, err := s.GetLatestPrice(context.Background(), uuid.New().String(), asset2.ID, "")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestListPriceHistory(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "HistoryAsset")
	baseAsset := createTestAsset(t, s, "HistoryBaseAsset")

	// Create prices with small delays to ensure ordering
	for i := 0; i < 5; i++ {
		createTestPrice(t, s, asset.ID, baseAsset.ID, "exchange")
		time.Sleep(10 * time.Millisecond)
	}

	t.Run("List price history", func(t *testing.T) {
		from := time.Now().Add(-time.Minute)
		to := time.Now()
		res, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
			From:        &from,
			To:          &to,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 5)
	})

	t.Run("List with non-existing asset", func(t *testing.T) {
		_, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     uuid.New().String(),
			BaseAssetID: baseAsset.ID,
		})
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Pagination", func(t *testing.T) {
		res, nextToken, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
			PageSize:    2,
		})
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.NotEmpty(t, nextToken)

		// Get next page
		res2, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
			PageSize:    2,
			PageToken:   nextToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res2)
	})
}

func TestDeletePrice(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "DeletePriceAsset1")
	baseAsset := createTestAsset(t, s, "DeletePriceAsset2")
	price := createTestPrice(t, s, asset.ID, baseAsset.ID, "exchange")

	t.Run("Delete price", func(t *testing.T) {
		err := s.DeletePrice(context.Background(), price.ID)
		assert.NoError(t, err)

		// Verify deletion
		_, err = s.GetLatestPrice(context.Background(), asset.ID, baseAsset.ID, "")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existing price", func(t *testing.T) {
		err := s.DeletePrice(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete with invalid ID", func(t *testing.T) {
		err := s.DeletePrice(context.Background(), "invalid-id")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestDeletePrices(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "DeletePricesAsset1")
	baseAsset := createTestAsset(t, s, "DeletePricesAsset2")

	for i := 0; i < 3; i++ {
		createTestPrice(t, s, asset.ID, baseAsset.ID, "exchange")
	}

	t.Run("Delete prices by asset", func(t *testing.T) {
		err := s.DeletePrices(context.Background(), marketdata.DeletePricesOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
		})
		assert.NoError(t, err)

		// Verify deletion
		_, err = s.GetLatestPrice(context.Background(), asset.ID, baseAsset.ID, "")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existing prices", func(t *testing.T) {
		err := s.DeletePrices(context.Background(), marketdata.DeletePricesOpts{
			AssetID:     uuid.New().String(),
			BaseAssetID: baseAsset.ID,
		})
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}
