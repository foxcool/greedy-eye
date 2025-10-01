package coingecko

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client implements PriceProvider interface for CoinGecko
type Client struct {
	apiKey  string
	baseURL string
	rateLimit time.Duration
}

// Config holds CoinGecko client configuration
type Config struct {
	APIKey string
	Pro    bool // Use Pro API endpoint
}

// PriceData represents price information for an asset
type PriceData struct {
	AssetID       string
	Symbol        string
	Price         float64
	MarketCap     float64
	Volume24h     float64
	Change24h     float64
	ChangePercent float64
	High24h       float64
	Low24h        float64
	Timestamp     time.Time
}

// HistoricalPrice represents historical price data point
type HistoricalPrice struct {
	Timestamp time.Time
	Price     float64
	Volume    float64
}

// NewClient creates a new CoinGecko price data client
func NewClient(cfg Config) *Client {
	baseURL := "https://api.coingecko.com/api/v3"
	rateLimit := 50 * time.Millisecond // Free tier: 10-30 calls/minute

	if cfg.Pro {
		baseURL = "https://pro-api.coingecko.com/api/v3"
		rateLimit = 10 * time.Millisecond // Pro tier: higher rate limits
	}

	return &Client{
		apiKey:    cfg.APIKey,
		baseURL:   baseURL,
		rateLimit: rateLimit,
	}
}

// GetCurrentPrice retrieves current price for an asset
func (c *Client) GetCurrentPrice(ctx context.Context, assetID string, currency string) (*PriceData, error) {
	return nil, status.Error(codes.Unimplemented, "GetCurrentPrice not implemented")
}

// GetMultiplePrices retrieves current prices for multiple assets
func (c *Client) GetMultiplePrices(ctx context.Context, assetIDs []string, currency string) (map[string]*PriceData, error) {
	return nil, status.Error(codes.Unimplemented, "GetMultiplePrices not implemented")
}

// GetHistoricalPrices retrieves historical price data
func (c *Client) GetHistoricalPrices(ctx context.Context, assetID string, currency string, from time.Time, to time.Time) ([]HistoricalPrice, error) {
	return nil, status.Error(codes.Unimplemented, "GetHistoricalPrices not implemented")
}

// GetMarketChart retrieves market chart data (price, volume, market cap)
func (c *Client) GetMarketChart(ctx context.Context, assetID string, currency string, days int) (interface{}, error) {
	return nil, status.Error(codes.Unimplemented, "GetMarketChart not implemented")
}

// SearchAssets searches for assets by name or symbol
func (c *Client) SearchAssets(ctx context.Context, query string) ([]interface{}, error) {
	return nil, status.Error(codes.Unimplemented, "SearchAssets not implemented")
}

// GetAssetDetails retrieves detailed information about an asset
func (c *Client) GetAssetDetails(ctx context.Context, assetID string) (interface{}, error) {
	return nil, status.Error(codes.Unimplemented, "GetAssetDetails not implemented")
}

// GetSupportedCurrencies retrieves list of supported vs currencies
func (c *Client) GetSupportedCurrencies(ctx context.Context) ([]string, error) {
	return nil, status.Error(codes.Unimplemented, "GetSupportedCurrencies not implemented")
}

// Ping checks if the API is reachable
func (c *Client) Ping(ctx context.Context) error {
	return status.Error(codes.Unimplemented, "Ping not implemented")
}
