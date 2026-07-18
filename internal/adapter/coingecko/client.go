package coingecko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
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
	// Spacing between consecutive requests inside one batched operation.
	// The keyless public API allows roughly 30 calls/minute per IP.
	rateLimit := 1200 * time.Millisecond
	if cfg.APIKey != "" {
		rateLimit = 100 * time.Millisecond // Demo/paid tiers: 30-500 calls/minute
	}

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

// evmAddressRe matches a canonical EVM contract address. Catalog tags may
// carry garbage from scam tokens (unicode tricks, truncated strings); anything
// not matching would poison the whole token_price batch with a 400.
var evmAddressRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// keylessMaxContractLookups caps per-address token_price requests in one
// call on the keyless tier, where each address costs a full request.
const keylessMaxContractLookups = 15

// GetTokenPricesByContract retrieves prices for ERC-20 tokens by their contract addresses.
// platform is the CoinGecko platform ID, e.g. "ethereum", "base", "polygon-pos".
//
// Malformed addresses are skipped up front, and a failed batch does not abort
// the rest: the returned map holds every price that was fetched, and the error
// (if any) aggregates per-batch failures. Callers should use both.
func (c *Client) GetTokenPricesByContract(ctx context.Context, platform string, addresses []string, currency string) (map[string]*PriceData, error) {
	seen := make(map[string]struct{}, len(addresses))
	valid := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		if !evmAddressRe.MatchString(addr) {
			continue
		}
		key := strings.ToLower(addr)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		valid = append(valid, addr)
	}
	if len(valid) == 0 {
		return map[string]*PriceData{}, nil
	}

	// With an API key (demo/pro) token_price accepts comma-separated batches;
	// the keyless public API rejects any request with more than one address
	// with a 400, so each address must go in its own request. To stay inside
	// the keyless per-minute budget (and not starve other endpoints), only a
	// random subset is attempted per call — repeated fetches rotate through
	// the full set.
	batchSize := 30
	if c.apiKey == "" {
		batchSize = 1
		if len(valid) > keylessMaxContractLookups {
			rand.Shuffle(len(valid), func(i, j int) { valid[i], valid[j] = valid[j], valid[i] })
			valid = valid[:keylessMaxContractLookups]
		}
	}
	result := make(map[string]*PriceData, len(valid))
	var errs []error

	for i := 0; i < len(valid); i += batchSize {
		if i > 0 {
			// Space consecutive requests to stay under the tier rate limit.
			select {
			case <-ctx.Done():
				return result, errors.Join(append(errs, ctx.Err())...)
			case <-time.After(c.rateLimit):
			}
		}
		end := min(i+batchSize, len(valid))
		batch := valid[i:end]

		url := fmt.Sprintf(
			"%s/simple/token_price/%s?contract_addresses=%s&vs_currencies=%s&include_24hr_high=true&include_24hr_low=true",
			c.baseURL,
			platform,
			strings.Join(batch, ","),
			currency,
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return result, fmt.Errorf("create request: %w", err)
		}
		if c.apiKey != "" {
			req.Header.Set("x-cg-demo-api-key", c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			errs = append(errs, fmt.Errorf("token_price batch %d-%d: %w", i, end, err))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			errs = append(errs, fmt.Errorf("token_price batch %d-%d: unexpected status %d from CoinGecko", i, end, resp.StatusCode))
			continue
		}

		// Response: { "0x...": { "usd": 1.23, "usd_24h_high": 1.30, "usd_24h_low": 1.10 }, ... }
		var raw map[string]map[string]float64
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			_ = resp.Body.Close()
			errs = append(errs, fmt.Errorf("token_price batch %d-%d: decode response: %w", i, end, err))
			continue
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

	return result, errors.Join(errs...)
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
