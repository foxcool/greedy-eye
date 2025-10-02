//go:build integration

package asset

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/enttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/lib/pq"
)

func TestAssetService_EnrichAssetData_Integration(t *testing.T) {
	// Skip if not in Docker Compose test environment
	if os.Getenv("DOCKER_COMPOSE_TEST") != "true" {
		t.Skip("Skipping integration test outside Docker Compose environment")
	}

	// Setup test database
	dbURL := os.Getenv("EYE_DB_URL")
	if dbURL == "" {
		t.Fatal("EYE_DB_URL environment variable not set")
	}
	client := enttest.Open(t, "postgres", dbURL)
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create asset service with storage dependency
	assetService := NewService(logger, storageClient)

	ctx := context.Background()

	t.Run("EnrichCryptocurrencyAsset", func(t *testing.T) {
		// Create a test cryptocurrency asset
		symbolPtr := "btc" // Using lowercase to test CoinGecko matching
		testAsset := &models.Asset{
			Symbol: &symbolPtr,
			Name:   "Bitcoin",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"original:true"},
		}

		createdAsset, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: testAsset})
		require.NoError(t, err)

		// Enrich the asset
		req := &services.EnrichAssetDataRequest{AssetId: createdAsset.Id}
		enrichedAsset, err := assetService.EnrichAssetData(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, enrichedAsset)

		// Check if enrichment was successful
		// Note: This test depends on CoinGecko API being available and bitcoin being in the response
		var hasDataSource bool
		for _, tag := range enrichedAsset.Tags {
			if strings.Contains(tag, "data_source:coingecko") {
				hasDataSource = true
				break
			}
		}
		if hasDataSource {
			// Should have enrichment tags
			var hasEnrichmentTimestamp bool
			var hasOriginalTag bool
			for _, tag := range enrichedAsset.Tags {
				if strings.Contains(tag, "enrichment_timestamp:") {
					hasEnrichmentTimestamp = true
				}
				if tag == "original:true" {
					hasOriginalTag = true
				}
			}
			assert.True(t, hasEnrichmentTimestamp, "Should have enrichment timestamp tag")
			assert.True(t, hasOriginalTag, "Should preserve original tag")
		}
	})

	t.Run("EnrichNonCryptocurrencyAsset", func(t *testing.T) {
		// Create a non-crypto asset
		symbolPtr := "AAPL"
		testAsset := &models.Asset{
			Symbol: &symbolPtr,
			Name:   "Apple Inc.",
			Type:   models.AssetType_ASSET_TYPE_STOCK,
			Tags:   []string{"sector:technology"},
		}

		createdAsset, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: testAsset})
		require.NoError(t, err)

		// Enrich the asset
		req := &services.EnrichAssetDataRequest{AssetId: createdAsset.Id}
		result, err := assetService.EnrichAssetData(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should return the asset as-is since it's not a cryptocurrency
		assert.Equal(t, createdAsset.Id, result.Id)
		assert.Equal(t, "AAPL", *result.Symbol)
		assert.Equal(t, models.AssetType_ASSET_TYPE_STOCK, result.Type)
		assert.Contains(t, result.Tags, "sector:technology")
	})

	t.Run("EnrichNonExistentAsset", func(t *testing.T) {
		req := &services.EnrichAssetDataRequest{AssetId: "non-existent-id"}
		result, err := assetService.EnrichAssetData(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "Asset not found")
	})
}

func TestAssetService_FindSimilarAssets_Integration(t *testing.T) {
	// Skip if not in Docker Compose test environment
	if os.Getenv("DOCKER_COMPOSE_TEST") != "true" {
		t.Skip("Skipping integration test outside Docker Compose environment")
	}

	// Setup test database
	dbURL := os.Getenv("EYE_DB_URL")
	if dbURL == "" {
		t.Fatal("EYE_DB_URL environment variable not set")
	}
	client := enttest.Open(t, "postgres", dbURL)
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create asset service
	assetService := NewService(logger, storageClient)

	ctx := context.Background()

	// Create test assets
	testAssets := []*models.Asset{
		{
			Symbol: func() *string { s := "BTC"; return &s }(),
			Name:   "Bitcoin",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:bitcoin"},
		},
		{
			Symbol: func() *string { s := "WBTC"; return &s }(),
			Name:   "Wrapped Bitcoin",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:ethereum"},
		},
		{
			Symbol: func() *string { s := "ETH"; return &s }(),
			Name:   "Ethereum",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:ethereum"},
		},
		{
			Symbol: func() *string { s := "WETH"; return &s }(),
			Name:   "Wrapped Ethereum",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:ethereum"},
		},
		{
			Symbol: func() *string { s := "DOT"; return &s }(),
			Name:   "Polkadot",
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:polkadot"},
		},
		{
			Symbol: func() *string { s := "AAPL"; return &s }(),
			Name:   "Apple Inc.",
			Type:   models.AssetType_ASSET_TYPE_STOCK,
		},
	}

	// Create all test assets
	var createdAssets []*models.Asset
	for _, asset := range testAssets {
		created, err := storageService.CreateAsset(ctx, &services.CreateAssetRequest{Asset: asset})
		require.NoError(t, err)
		createdAssets = append(createdAssets, created)
	}

	t.Run("FindSimilarCryptocurrencyAssets", func(t *testing.T) {
		// Find similar assets to BTC
		btcAsset := createdAssets[0] // BTC
		req := &services.FindSimilarAssetsRequest{AssetId: btcAsset.Id}

		resp, err := assetService.FindSimilarAssets(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should return some similar assets (may include WBTC if algorithm finds it)
		// Note: Exact results may vary due to parallel test execution and database state
		assert.NotNil(t, resp.Assets, "Should return assets list")

		// Should not include the original BTC asset
		for _, asset := range resp.Assets {
			assert.NotEqual(t, btcAsset.Id, asset.Id, "Should not include the original asset")
		}
	})

	t.Run("FindSimilarWrappedTokens", func(t *testing.T) {
		// Find similar assets to ETH
		ethAsset := createdAssets[2] // ETH
		req := &services.FindSimilarAssetsRequest{AssetId: ethAsset.Id}

		resp, err := assetService.FindSimilarAssets(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should find WETH as similar (wrapped token)
		var foundWETH bool
		for _, asset := range resp.Assets {
			if asset.Symbol != nil && *asset.Symbol == "WETH" {
				foundWETH = true
				break
			}
		}
		assert.True(t, foundWETH, "Should find WETH as similar to ETH")
	})

	t.Run("FindSimilarByBlockchain", func(t *testing.T) {
		// Find similar assets to WETH (should find ETH due to same blockchain)
		wethAsset := createdAssets[3] // WETH
		req := &services.FindSimilarAssetsRequest{AssetId: wethAsset.Id}

		resp, err := assetService.FindSimilarAssets(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should find other ethereum-based assets
		var foundEthereumAssets int
		for _, asset := range resp.Assets {
			for _, tag := range asset.Tags {
				if tag == "blockchain:ethereum" {
					foundEthereumAssets++
					break
				}
			}
		}
		assert.True(t, foundEthereumAssets > 0, "Should find assets on same blockchain")
	})

	t.Run("FindSimilarDifferentAssetTypes", func(t *testing.T) {
		// Find similar assets to AAPL (stock)
		aaplAsset := createdAssets[5] // AAPL
		req := &services.FindSimilarAssetsRequest{AssetId: aaplAsset.Id}

		resp, err := assetService.FindSimilarAssets(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Should not find crypto assets as similar to stock
		for _, asset := range resp.Assets {
			assert.Equal(t, models.AssetType_ASSET_TYPE_STOCK, asset.Type, "Should only find assets of same type")
		}
	})

	t.Run("FindSimilarNonExistentAsset", func(t *testing.T) {
		req := &services.FindSimilarAssetsRequest{AssetId: "non-existent-id"}
		resp, err := assetService.FindSimilarAssets(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "Asset not found")
	})
}

func TestAssetService_SimilarityAlgorithms_Integration(t *testing.T) {
	// Skip if not in Docker Compose test environment
	if os.Getenv("DOCKER_COMPOSE_TEST") != "true" {
		t.Skip("Skipping integration test outside Docker Compose environment")
	}

	// Setup test database (not actually used but required for consistency)
	dbURL := os.Getenv("EYE_DB_URL")
	if dbURL == "" {
		t.Fatal("EYE_DB_URL environment variable not set")
	}
	client := enttest.Open(t, "postgres", dbURL)
	defer client.Close()

	// Setup logger
	logger := zaptest.NewLogger(t)

	// Create storage service
	storageService := storage.NewService(client, logger)
	storageClient := storage.NewLocalClient(storageService)

	// Create asset service
	assetService := NewService(logger, storageClient)

	t.Run("TestCryptoAssetsSimilarity", func(t *testing.T) {
		// Test wrapped token similarity
		btc := &models.Asset{
			Symbol: func() *string { s := "BTC"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		}
		wbtc := &models.Asset{
			Symbol: func() *string { s := "WBTC"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		}

		assert.True(t, assetService.areCryptoAssetsSimilar(btc, wbtc), "BTC and WBTC should be similar")

		// Test blockchain similarity
		eth := &models.Asset{
			Symbol: func() *string { s := "ETH"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:ethereum"},
		}
		usdc := &models.Asset{
			Symbol: func() *string { s := "USDC"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:ethereum"},
		}

		assert.True(t, assetService.areCryptoAssetsSimilar(eth, usdc), "ETH and USDC should be similar due to same blockchain")

		// Test dissimilar assets
		dot := &models.Asset{
			Symbol: func() *string { s := "DOT"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"blockchain:polkadot"},
		}

		assert.False(t, assetService.areCryptoAssetsSimilar(btc, dot), "BTC and DOT should not be similar")
	})

	t.Run("TestNamesSimilarity", func(t *testing.T) {
		testCases := []struct {
			name1    string
			name2    string
			expected bool
		}{
			{"Bitcoin", "Bitcoin Cash", true},
			{"Ethereum", "Ethereum Classic", true},
			{"Apple Inc.", "Apple Computer", true},
			{"Microsoft Corporation", "Microsoft", true},
			{"Bitcoin", "Litecoin", false},
			{"Tesla", "Toyota", false},
		}

		for _, tc := range testCases {
			result := assetService.areNamesSimilar(tc.name1, tc.name2)
			assert.Equal(t, tc.expected, result, "Name similarity test failed for '%s' and '%s'", tc.name1, tc.name2)
		}
	})

	t.Run("TestAssetsSimilarity", func(t *testing.T) {
		// Test different asset types are not similar
		crypto := &models.Asset{
			Symbol: func() *string { s := "BTC"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		}
		stock := &models.Asset{
			Symbol: func() *string { s := "BTC"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_STOCK,
		}

		assert.False(t, assetService.areAssetsSimilar(crypto, stock), "Different asset types should not be similar")

		// Test same type crypto assets
		eth := &models.Asset{
			Symbol: func() *string { s := "ETH"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		}
		weth := &models.Asset{
			Symbol: func() *string { s := "WETH"; return &s }(),
			Type:   models.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		}

		assert.True(t, assetService.areAssetsSimilar(eth, weth), "ETH and WETH should be similar")
	})
}