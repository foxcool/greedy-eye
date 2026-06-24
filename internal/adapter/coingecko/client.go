package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client implements PriceProvider interface for CoinGecko
type Client struct {
	apiKey     string
	baseURL    string
	rateLimit  time.Duration
	httpClient *http.Client
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
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		rateLimit:  rateLimit,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// coingeckoMarketItem is the JSON shape from /coins/markets endpoint.
type coingeckoMarketItem struct {
	ID                string  `json:"id"`
	Symbol            string  `json:"symbol"`
	CurrentPrice      float64 `json:"current_price"`
	MarketCap         float64 `json:"market_cap"`
	TotalVolume       float64 `json:"total_volume"`
	PriceChange24h    float64 `json:"price_change_24h"`
	PriceChangePct24h float64 `json:"price_change_percentage_24h"`
	High24h           float64 `json:"high_24h"`
	Low24h            float64 `json:"low_24h"`
}

// GetMultiplePrices retrieves current prices for multiple assets.
func (c *Client) GetMultiplePrices(ctx context.Context, assetIDs []string, currency string) (map[string]*PriceData, error) {
	if len(assetIDs) == 0 {
		return map[string]*PriceData{}, nil
	}

	url := fmt.Sprintf(
		"%s/coins/markets?vs_currency=%s&ids=%s&order=market_cap_desc&per_page=250&page=1&sparkline=false",
		c.baseURL,
		currency,
		strings.Join(assetIDs, ","),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("x-cg-demo-api-key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from CoinGecko", resp.StatusCode)
	}

	var items []coingeckoMarketItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	now := time.Now()
	result := make(map[string]*PriceData, len(items))
	for _, item := range items {
		result[item.ID] = &PriceData{
			AssetID:       item.ID,
			Symbol:        item.Symbol,
			Price:         item.CurrentPrice,
			MarketCap:     item.MarketCap,
			Volume24h:     item.TotalVolume,
			Change24h:     item.PriceChange24h,
			ChangePercent: item.PriceChangePct24h,
			High24h:       item.High24h,
			Low24h:        item.Low24h,
			Timestamp:     now,
		}
	}

	return result, nil
}

// GetTokenPricesByContract retrieves prices for ERC-20 tokens by their contract addresses.
// platform is the CoinGecko platform ID, e.g. "ethereum", "base", "polygon-pos".
func (c *Client) GetTokenPricesByContract(ctx context.Context, platform string, addresses []string, currency string) (map[string]*PriceData, error) {
	if len(addresses) == 0 {
		return map[string]*PriceData{}, nil
	}

	// CoinGecko supports batching up to 30 addresses per request on free tier.
	const batchSize = 30
	result := make(map[string]*PriceData, len(addresses))

	for i := 0; i < len(addresses); i += batchSize {
		end := i + batchSize
		if end > len(addresses) {
			end = len(addresses)
		}
		batch := addresses[i:end]

		url := fmt.Sprintf(
			"%s/simple/token_price/%s?contract_addresses=%s&vs_currencies=%s&include_24hr_high=true&include_24hr_low=true",
			c.baseURL,
			platform,
			strings.Join(batch, ","),
			currency,
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		if c.apiKey != "" {
			req.Header.Set("x-cg-demo-api-key", c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("do request: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %d from CoinGecko token_price", resp.StatusCode)
		}

		// Response: { "0x...": { "usd": 1.23, "usd_24h_high": 1.30, "usd_24h_low": 1.10 }, ... }
		var raw map[string]map[string]float64
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		_ = resp.Body.Close()

		now := time.Now()
		for addr, prices := range raw {
			price := prices[currency]
			if price == 0 {
				continue
			}
			result[addr] = &PriceData{
				Price:     price,
				High24h:   prices[currency+"_24h_high"],
				Low24h:    prices[currency+"_24h_low"],
				Timestamp: now,
			}
		}
	}

	return result, nil
}

// GetCurrentPrice retrieves current price for an asset.
func (c *Client) GetCurrentPrice(ctx context.Context, assetID string, currency string) (*PriceData, error) {
	prices, err := c.GetMultiplePrices(ctx, []string{assetID}, currency)
	if err != nil {
		return nil, err
	}
	p, ok := prices[assetID]
	if !ok {
		return nil, fmt.Errorf("asset %q not found in CoinGecko response", assetID)
	}
	return p, nil
}

// GetHistoricalPrices retrieves historical price data (stub).
func (c *Client) GetHistoricalPrices(ctx context.Context, assetID string, currency string, from time.Time, to time.Time) ([]HistoricalPrice, error) {
	return nil, fmt.Errorf("GetHistoricalPrices not implemented")
}

// GetMarketChart retrieves market chart data (stub).
func (c *Client) GetMarketChart(ctx context.Context, assetID string, currency string, days int) (interface{}, error) {
	return nil, fmt.Errorf("GetMarketChart not implemented")
}

// SearchAssets searches for assets by name or symbol (stub).
func (c *Client) SearchAssets(ctx context.Context, query string) ([]interface{}, error) {
	return nil, fmt.Errorf("SearchAssets not implemented")
}

// GetAssetDetails retrieves detailed information about an asset (stub).
func (c *Client) GetAssetDetails(ctx context.Context, assetID string) (interface{}, error) {
	return nil, fmt.Errorf("GetAssetDetails not implemented")
}

// GetSupportedCurrencies retrieves list of supported vs currencies (stub).
func (c *Client) GetSupportedCurrencies(ctx context.Context) ([]string, error) {
	return nil, fmt.Errorf("GetSupportedCurrencies not implemented")
}

// Ping checks if the API is reachable.
func (c *Client) Ping(ctx context.Context) error {
	url := c.baseURL + "/ping"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping failed with status %d", resp.StatusCode)
	}
	return nil
}
