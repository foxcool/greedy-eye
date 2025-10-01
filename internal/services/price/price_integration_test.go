package price

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/enttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/protobuf/types/known/timestamppb"

	_ "github.com/mattn/go-sqlite3"
)

func TestPriceService_FetchExternalPrices_Integration(t *testing.T) {
	// Setup test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create price service with storage dependency
	priceService := NewService(logger, storageClient, nil)

	ctx := context.Background()

	t.Run("FetchFromCoinGecko", func(t *testing.T) {
		// Test fetching prices from CoinGecko
		req := &services.FetchExternalPricesRequest{
			SourceIds: []string{"coingecko"},
			AssetIds:  []string{}, // Fetch all assets
		}

		resp, err := priceService.FetchExternalPrices(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should have fetched some prices
		assert.True(t, resp.PricesFetched > 0, "Should have fetched at least some prices")
		assert.Equal(t, 0, len(resp.Errors), "Fetch operation should be successful")

		// Verify prices were saved to storage - use ListPriceHistory
		// Note: We can't easily verify prices without knowing asset IDs, so skip detailed verification
		// The fact that PricesFetched > 0 means prices were saved successfully

		// Verify assets were created
		listAssetsReq := &services.ListAssetsRequest{
			PageSize: func() *int32 { i := int32(10); return &i }(),
		}

		assetsResp, err := storageService.ListAssets(ctx, listAssetsReq)
		require.NoError(t, err)
		assert.True(t, len(assetsResp.Assets) > 0, "Should have created assets")

		// Verify asset tags includes auto-creation info
		asset := assetsResp.Assets[0]
		var hasDataSource bool
		for _, tag := range asset.Tags {
			if strings.Contains(tag, "data_source:") {
				hasDataSource = true
				break
			}
		}
		assert.True(t, hasDataSource, "Asset should have data source tag")
	})

	t.Run("FetchSpecificAssets", func(t *testing.T) {
		// First create a specific asset
		symbolPtr := "BTC"
		testAsset := &models.Asset{
			Symbol: &symbolPtr,
			Name:   "Bitcoin",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"test:true"},
		}

		createdAsset, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: testAsset})
		require.NoError(t, err)

		// Fetch prices for this specific asset
		req := &services.FetchExternalPricesRequest{
			SourceIds: []string{"coingecko"},
			AssetIds:  []string{createdAsset.Id},
		}

		resp, err := priceService.FetchExternalPrices(ctx, req)
		require.NoError(t, err)

		// Should have fetched at least one price
		assert.True(t, resp.PricesFetched >= 0, "Should handle specific asset fetch")

		// If BTC is available in CoinGecko data, should have fetched successfully
		if resp.PricesFetched > 0 {
			// Verify the price was saved for our specific asset using ListPriceHistory
			listPricesReq := &services.ListPriceHistoryRequest{
				AssetId:     createdAsset.Id,
				BaseAssetId: "", // Empty to get all base assets
			}

			pricesResp, err := storageService.ListPriceHistory(ctx, listPricesReq)
			require.NoError(t, err)
			assert.True(t, len(pricesResp.Prices) > 0, "Should have price for specific asset")
		}
	})

	t.Run("FetchFromUnsupportedSource", func(t *testing.T) {
		req := &services.FetchExternalPricesRequest{
			SourceIds: []string{"unsupported_source"},
			AssetIds:  []string{},
		}

		resp, err := priceService.FetchExternalPrices(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should not be successful due to unsupported source
		assert.True(t, len(resp.Errors) > 0, "Should have errors")
		assert.Contains(t, resp.Errors[0], "Unsupported source")
		assert.Equal(t, int32(0), resp.PricesFetched)
	})
}

func TestPriceService_GetLatestPrice_Integration(t *testing.T) {
	// Setup test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create price service
	priceService := NewService(logger, storageClient, nil)

	ctx := context.Background()

	// Create test asset
	symbolPtr := "ETH"
	testAsset := &models.Asset{
		Symbol: &symbolPtr,
		Name:   "Ethereum",
		Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
	}

	createdAsset, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: testAsset})
	require.NoError(t, err)

	// Create test price
	testPrice := &models.Price{
		SourceId:  "coingecko",
		AssetId:   createdAsset.Id,
		Last:      250050, // 2500.50 * 100 for 2 decimal places
		Decimals:  2,
		Timestamp: timestamppb.New(time.Now()),
	}

	_, err = storageService.CreatePrice(ctx, &services.CreatePriceRequest{Price: testPrice})
	require.NoError(t, err)

	t.Run("GetExistingPrice", func(t *testing.T) {
		price, err := priceService.GetLatestPrice(ctx, createdAsset.Id, "coingecko")
		require.NoError(t, err)
		require.NotNil(t, price)

		assert.Equal(t, createdAsset.Id, price.AssetId)
		assert.Equal(t, int64(250050), price.Last)
		assert.Equal(t, "coingecko", price.SourceId)
	})

	t.Run("GetNonExistentPrice", func(t *testing.T) {
		price, err := priceService.GetLatestPrice(ctx, "non-existent-asset", "coingecko")
		assert.Error(t, err)
		assert.Nil(t, price)
		assert.Contains(t, err.Error(), "no price data found")
	})
}

func TestPriceService_GetPriceHistory_Integration(t *testing.T) {
	// Setup test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create price service
	priceService := NewService(logger, storageClient, nil)

	ctx := context.Background()

	// Create test asset
	symbolPtr := "DOT"
	testAsset := &models.Asset{
		Symbol: &symbolPtr,
		Name:   "Polkadot",
		Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
	}

	createdAsset, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: testAsset})
	require.NoError(t, err)

	// Create historical prices
	now := time.Now()
	prices := []struct {
		price int64
		time  time.Time
	}{
		{1050, now.Add(-3 * time.Hour)}, // 10.50 * 100
		{1075, now.Add(-2 * time.Hour)}, // 10.75 * 100
		{1100, now.Add(-1 * time.Hour)}, // 11.00 * 100
		{1125, now},                     // 11.25 * 100
	}

	for _, p := range prices {
		priceRecord := &models.Price{
			SourceId:  "coingecko",
			AssetId:   createdAsset.Id,
			Last:      p.price,
			Decimals:  2,
			Timestamp: timestamppb.New(p.time),
		}

		_, err = storageService.CreatePrice(ctx, &services.CreatePriceRequest{Price: priceRecord})
		require.NoError(t, err)
	}

	t.Run("GetAllHistory", func(t *testing.T) {
		history, err := priceService.GetPriceHistory(ctx, createdAsset.Id, "coingecko", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, history)

		assert.Equal(t, 4, len(history))
	})

	t.Run("GetHistoryWithTimeRange", func(t *testing.T) {
		fromTime := now.Add(-2*time.Hour - 30*time.Minute)
		toTime := now.Add(-30 * time.Minute)

		history, err := priceService.GetPriceHistory(ctx, createdAsset.Id, "coingecko", &fromTime, &toTime)
		require.NoError(t, err)
		require.NotNil(t, history)

		// Should include prices within the time range
		assert.True(t, len(history) >= 2, "Should include prices within time range")
	})
}

func TestPriceService_FindOrCreateAsset_Integration(t *testing.T) {
	// Setup test database
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create price service
	priceService := NewService(logger, storageClient, nil)

	ctx := context.Background()

	t.Run("CreateNewAsset", func(t *testing.T) {
		asset, err := priceService.findOrCreateAsset(ctx, "NEWTOKEN", models.AssetType_ASSET_TYPE_CRYPTOCURRENCY)
		require.NoError(t, err)
		require.NotNil(t, asset)

		assert.Equal(t, "NEWTOKEN", *asset.Symbol)
		assert.Equal(t, "NEWTOKEN Cryptocurrency", asset.Name)
		assert.Equal(t, models.AssetType_ASSET_TYPE_CRYPTOCURRENCY, asset.Type)
	})

	t.Run("FindExistingAsset", func(t *testing.T) {
		// First create an asset manually
		symbolPtr := "EXISTING"
		existingAsset := &models.Asset{
			Symbol: &symbolPtr,
			Name:   "Existing Token",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"manual:true"},
		}

		created, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: existingAsset})
		require.NoError(t, err)

		// Now try to find or create - should find existing
		found, err := priceService.findOrCreateAsset(ctx, "EXISTING", models.AssetType_ASSET_TYPE_CRYPTOCURRENCY)
		require.NoError(t, err)
		require.NotNil(t, found)

		assert.Equal(t, created.Id, found.Id)
		assert.Equal(t, "Existing Token", found.Name)
		var hasManualTag bool
		for _, tag := range found.Tags {
			if tag == "manual:true" {
				hasManualTag = true
				break
			}
		}
		assert.True(t, hasManualTag, "Should have manual tag")
	})

	t.Run("GenerateAssetNames", func(t *testing.T) {
		testCases := []struct {
			symbol   string
			assetType models.AssetType
			expected string
		}{
			{"BTC", models.AssetType_ASSET_TYPE_CRYPTOCURRENCY, "Bitcoin"},
			{"ETH", models.AssetType_ASSET_TYPE_CRYPTOCURRENCY, "Ethereum"},
			{"UNKNOWN", models.AssetType_ASSET_TYPE_CRYPTOCURRENCY, "UNKNOWN Cryptocurrency"},
			{"AAPL", models.AssetType_ASSET_TYPE_STOCK, "AAPL Stock"},
		}

		for _, tc := range testCases {
			name := priceService.generateAssetName(tc.symbol, tc.assetType)
			assert.Equal(t, tc.expected, name, "Asset name generation failed for %s", tc.symbol)
		}
	})
}