package price

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/coingecko"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PriceService struct {
	log           *zap.Logger
	storageClient services.StorageServiceClient
	assetClient   services.AssetServiceClient
	coinGecko     *coingecko.Service
}

func NewService(logger *zap.Logger, storageClient services.StorageServiceClient, assetClient services.AssetServiceClient) *PriceService {
	return &PriceService{
		log:           logger,
		storageClient: storageClient,
		assetClient:   assetClient,
		coinGecko:     &coingecko.Service{},
	}
}

// FetchExternalPrices triggers fetching of latest prices from external sources
func (s *PriceService) FetchExternalPrices(ctx context.Context, req *services.FetchExternalPricesRequest) (*services.FetchExternalPricesResponse, error) {
	s.log.Info("FetchExternalPrices called",
		zap.Strings("source_ids", req.SourceIds),
		zap.Strings("asset_ids", req.AssetIds))

	var fetchedCount int64
	var errors []string

	// Process each requested source
	for _, sourceID := range req.SourceIds {
		switch strings.ToLower(sourceID) {
		case "coingecko":
			count, err := s.fetchFromCoinGecko(ctx, req.AssetIds)
			if err != nil {
				s.log.Error("Failed to fetch from CoinGecko", zap.Error(err))
				errors = append(errors, fmt.Sprintf("CoinGecko: %v", err))
			} else {
				fetchedCount += count
				s.log.Info("Successfully fetched from CoinGecko", zap.Int64("count", count))
			}

		case "binance":
			count, err := s.fetchFromBinance(ctx, req.AssetIds)
			if err != nil {
				s.log.Error("Failed to fetch from Binance", zap.Error(err))
				errors = append(errors, fmt.Sprintf("Binance: %v", err))
			} else {
				fetchedCount += count
				s.log.Info("Successfully fetched from Binance", zap.Int64("count", count))
			}

		default:
			err := fmt.Sprintf("Unsupported source: %s", sourceID)
			s.log.Warn(err)
			errors = append(errors, err)
		}
	}

	response := &services.FetchExternalPricesResponse{
		PricesFetched: int32(fetchedCount),
		PricesStored:  int32(fetchedCount), // Assuming all fetched prices are stored successfully
		Errors:        errors,
	}

	s.log.Info("FetchExternalPrices completed",
		zap.Int64("fetched_count", fetchedCount),
		zap.Bool("success", len(errors) == 0),
		zap.Strings("errors", response.Errors))

	return response, nil
}

// fetchFromCoinGecko fetches price data from CoinGecko
func (s *PriceService) fetchFromCoinGecko(ctx context.Context, assetIDs []string) (int64, error) {
	// Get price data from CoinGecko
	priceData, err := coingecko.Get()
	if err != nil {
		return 0, fmt.Errorf("failed to get CoinGecko data: %w", err)
	}

	var savedCount int64

	// If specific asset IDs provided, filter accordingly
	if len(assetIDs) > 0 {
		assetSymbolSet := make(map[string]bool)
		for _, assetID := range assetIDs {
			// Get asset to find symbol
			asset, err := s.storageClient.GetAsset(ctx, &services.GetAssetRequest{Id: assetID})
			if err != nil {
				s.log.Warn("Failed to get asset", zap.String("asset_id", assetID), zap.Error(err))
				continue
			}
			if asset.Symbol != nil {
				assetSymbolSet[strings.ToLower(*asset.Symbol)] = true
			}
		}

		// Filter price data by requested assets
		var filteredPriceData []coingecko.CoinData
		for _, price := range priceData {
			if assetSymbolSet[strings.ToLower(string(price.BaseAsset))] {
				// Convert to CoinData structure (simplified for this example)
				coinData := coingecko.CoinData{
					Symbol:       string(price.BaseAsset),
					CurrentPrice: price.LastPrice.InexactFloat64(),
					LastUpdated:  price.Time,
				}
				filteredPriceData = append(filteredPriceData, coinData)
			}
		}
		priceData = nil // Clear original data
	}

	// Save price data to storage
	for _, price := range priceData {
		// Find or create asset
		asset, err := s.findOrCreateAsset(ctx, string(price.BaseAsset), models.AssetType_ASSET_TYPE_CRYPTOCURRENCY)
		if err != nil {
			s.log.Warn("Failed to find/create asset", zap.String("symbol", string(price.BaseAsset)), zap.Error(err))
			continue
		}

		// Create price record
		priceRecord := &models.Price{
			SourceId:  "coingecko",
			AssetId:   asset.Id,
			Last:      price.LastPrice.IntPart(),
			Timestamp: timestamppb.New(price.Time),
		}

		// Save to storage
		_, err = s.storageClient.CreatePrice(ctx, &services.CreatePriceRequest{Price: priceRecord})
		if err != nil {
			s.log.Warn("Failed to save price", zap.String("asset_id", asset.Id), zap.Error(err))
			continue
		}

		savedCount++
	}

	return savedCount, nil
}

// fetchFromBinance fetches price data from Binance (placeholder implementation)
func (s *PriceService) fetchFromBinance(ctx context.Context, assetIDs []string) (int64, error) {
	// TODO: Implement Binance API integration
	// This would require:
	// 1. Binance API client setup
	// 2. User API key management
	// 3. Price ticker data fetching
	// 4. Data conversion and storage

	s.log.Info("Binance price fetching not yet implemented")
	return 0, fmt.Errorf("Binance integration not implemented")
}

// findOrCreateAsset finds an existing asset by symbol or creates a new one
func (s *PriceService) findOrCreateAsset(ctx context.Context, symbol string, assetType models.AssetType) (*models.Asset, error) {
	// Try to find existing asset by listing all and filtering
	// Note: This is not optimal but works with current proto structure
	listReq := &services.ListAssetsRequest{
		PageSize: func() *int32 { i := int32(100); return &i }(),
	}

	response, err := s.storageClient.ListAssets(ctx, listReq)
	if err != nil {
		return nil, fmt.Errorf("failed to search for asset: %w", err)
	}

	// Search for existing asset by symbol and type
	for _, asset := range response.Assets {
		if asset.Symbol != nil && strings.EqualFold(*asset.Symbol, symbol) && asset.Type == assetType {
			return asset, nil
		}
	}

	// Create new asset
	symbolPtr := symbol
	newAsset := &models.Asset{
		Symbol: &symbolPtr,
		Name:   s.generateAssetName(symbol, assetType),
		Type:   assetType,
	}

	createdAsset, err := s.storageClient.CreateAsset(ctx, &services.CreateAssetRequest{Asset: newAsset})
	if err != nil {
		return nil, fmt.Errorf("failed to create asset: %w", err)
	}

	s.log.Info("Created new asset", zap.String("symbol", symbol), zap.String("asset_id", createdAsset.Id))
	return createdAsset, nil
}

// generateAssetName generates a human-readable name for an asset
func (s *PriceService) generateAssetName(symbol string, assetType models.AssetType) string {
	symbol = strings.ToUpper(symbol)

	// Known cryptocurrency names mapping
	knownNames := map[string]string{
		"BTC":  "Bitcoin",
		"ETH":  "Ethereum",
		"DOT":  "Polkadot",
		"TON":  "The Open Network",
		"DAI":  "Dai Stablecoin",
		"USDC": "USD Coin",
		"UNI":  "Uniswap",
		"1INCH": "1inch",
		"GLMR": "Moonbeam",
		"OP":   "Optimism",
		"KSM":  "Kusama",
		"XTZ":  "Tezos",
		"AAVE": "Aave",
		"ENS":  "Ethereum Name Service",
		"GTC":  "Gitcoin",
		"MKR":  "Maker",
		"BNB":  "BNB",
		"USDT": "Tether",
	}

	if name, exists := knownNames[symbol]; exists {
		return name
	}

	// Generate generic name based on type
	switch assetType {
	case models.AssetType_ASSET_TYPE_CRYPTOCURRENCY:
		return fmt.Sprintf("%s Cryptocurrency", symbol)
	case models.AssetType_ASSET_TYPE_STOCK:
		return fmt.Sprintf("%s Stock", symbol)
	case models.AssetType_ASSET_TYPE_BOND:
		return fmt.Sprintf("%s Bond", symbol)
	default:
		return fmt.Sprintf("%s Asset", symbol)
	}
}

// GetLatestPrice retrieves the latest price for an asset from storage
func (s *PriceService) GetLatestPrice(ctx context.Context, assetID string, source string) (*models.Price, error) {
	// Use ListPriceHistory with empty base_asset_id to get all prices for asset
	listReq := &services.ListPriceHistoryRequest{
		AssetId:     assetID,
		BaseAssetId: "", // Empty to get all base assets
	}

	response, err := s.storageClient.ListPriceHistory(ctx, listReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest price: %w", err)
	}

	// Filter by source and find latest
	var latestPrice *models.Price
	for _, price := range response.Prices {
		if price.SourceId == source {
			if latestPrice == nil || price.Timestamp.AsTime().After(latestPrice.Timestamp.AsTime()) {
				latestPrice = price
			}
		}
	}

	if latestPrice == nil {
		return nil, fmt.Errorf("no price data found for asset %s from source %s", assetID, source)
	}

	return latestPrice, nil
}

// GetPriceHistory retrieves historical prices for an asset
func (s *PriceService) GetPriceHistory(ctx context.Context, assetID string, source string, fromTime, toTime *time.Time) ([]*models.Price, error) {
	listReq := &services.ListPriceHistoryRequest{
		AssetId:     assetID,
		BaseAssetId: "", // Empty to get all base assets
	}

	// Add time filters if provided
	if fromTime != nil {
		listReq.From = timestamppb.New(*fromTime)
	}
	if toTime != nil {
		listReq.To = timestamppb.New(*toTime)
	}

	response, err := s.storageClient.ListPriceHistory(ctx, listReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get price history: %w", err)
	}

	// Filter by source if specified
	if source == "" {
		return response.Prices, nil
	}

	var filteredPrices []*models.Price
	for _, price := range response.Prices {
		if price.SourceId == source {
			filteredPrices = append(filteredPrices, price)
		}
	}

	return filteredPrices, nil
}
