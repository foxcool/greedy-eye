package coingecko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Client implements PriceProvider interface for CoinGecko
type Client struct {
	apiKey string
	// authHeader is the header name this plan authenticates with. The paid API
	// rejects the demo header and vice versa, so it is decided once, from the
	// same plan that picked the host.
	authHeader string
	baseURL    string
	httpClient *http.Client
	budget     PlanBudget
}

// PlanBudget reports what is left of this credential's plan for the current
// period. Satisfied by *ratelimit.Budget; the interface keeps the adapter from
// depending on how budgets are keyed.
type PlanBudget interface {
	Remaining() (requests int, periodEnd time.Time, ok bool)
	// Unusable reports why this credential cannot carry background work now.
	// A sweep asks before selecting anything: being refused once per asset
	// costs a request each time, and every refusal is filed against the ASSET,
	// so its own back-off grows because of an allowance it never touched.
	Unusable() (reason string, unusable bool)
}

// noBudget stands in when none was wired: unmetered by volume, so callers that
// size themselves from a plan simply do not.
type noBudget struct{}

func (noBudget) Remaining() (int, time.Time, bool) { return 0, time.Time{}, false }

func (noBudget) Unusable() (string, bool) { return "", false }

// TierPro names CoinGecko's paid plans. It matches the tier an account carries
// in data["tier"], which is also what the rate budget resolves its limits from:
// one name has to pick the host, the auth header and the allowance together, or
// a key ends up talking to the free host under a paid plan's rate.
const TierPro = "pro"

// Config holds CoinGecko client configuration
type Config struct {
	APIKey string
	// Tier names the plan the key is on ("pro" for the paid API, empty for the
	// free one). It selects the host and the auth header.
	Tier string
	// Pro is the older spelling of Tier == TierPro, still accepted.
	Pro bool

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper

	// Budget, when set, lets an unattended sweep size itself from what is left
	// of the plan's monthly allowance instead of a hardcoded cap.
	Budget PlanBudget
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
	// Host, header and plan are one decision. Splitting them is how a key with
	// tier "pro" reached the free host with the demo header while the rate
	// budget handed it the paid plan's ceiling — every request a 429.
	baseURL := "https://api.coingecko.com/api/v3"
	authHeader := "x-cg-demo-api-key"
	if cfg.Pro || cfg.Tier == TierPro {
		baseURL = "https://pro-api.coingecko.com/api/v3"
		authHeader = "x-cg-pro-api-key"
	}

	// Request spacing is not this client's business any more: it used to
	// pace itself, which paced one instance while the resolver built others
	// against the same key. The budget now lives in the transport
	// (internal/adapter/ratelimit), shared per credential.
	budget := cfg.Budget
	if budget == nil {
		budget = noBudget{}
	}
	return &Client{
		apiKey:     cfg.APIKey,
		authHeader: authHeader,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
		budget:     budget,
	}
}

// authenticate names the key on a request. Keyless calls carry no header at
// all: the public tier is metered per IP and rejects an empty key value.
func (c *Client) authenticate(req *http.Request) {
	if c.apiKey == "" {
		return
	}
	req.Header.Set(c.authHeader, c.apiKey)
}

// contractBatchSize is how many contract addresses fit in one token_price
// request at this credential's tier. The keyless API rejects more than one
// address per request with a 400.
func (c *Client) contractBatchSize() int {
	if c.apiKey == "" {
		return 1
	}
	return keyedContractBatch
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

// marketsPageSize is how many coin ids one /coins/markets response can carry.
// Asking for more silently truncates, which is worse than a second request.
const marketsPageSize = 250

// GetMultiplePrices retrieves current prices for multiple assets.
func (c *Client) GetMultiplePrices(ctx context.Context, assetIDs []string, currency string) (map[string]*PriceData, error) {
	if len(assetIDs) == 0 {
		return map[string]*PriceData{}, nil
	}

	result := make(map[string]*PriceData, len(assetIDs))
	for chunk := range slices.Chunk(assetIDs, marketsPageSize) {
		if err := c.fetchMarkets(ctx, chunk, currency, result); err != nil {
			return result, err
		}
	}
	return result, nil
}

// fetchMarkets reads one page of /coins/markets into result.
func (c *Client) fetchMarkets(ctx context.Context, assetIDs []string, currency string, result map[string]*PriceData) error {
	url := fmt.Sprintf(
		"%s/coins/markets?vs_currency=%s&ids=%s&order=market_cap_desc&per_page=%d&page=1&sparkline=false",
		c.baseURL,
		currency,
		strings.Join(assetIDs, ","),
		marketsPageSize,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.authenticate(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from CoinGecko", resp.StatusCode)
	}

	var items []coingeckoMarketItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	now := time.Now()
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

	return nil
}

// evmAddressRe matches a canonical EVM contract address. Catalog tags may
// carry garbage from scam tokens (unicode tricks, truncated strings); anything
// not matching would poison the whole token_price batch with a 400.
var evmAddressRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

const (
	// keylessMaxContractLookups caps per-address token_price requests in one
	// call on the keyless tier, where each address costs a full request.
	keylessMaxContractLookups = 15
	// keyedContractBatch is how many contract addresses a demo or paid key may
	// put in one token_price request.
	keyedContractBatch = 30
)

// backoffStatuses are the answers that mean "stop sending for a while": 429 is
// the standard one, 418 is Binance-style, 430 is Blockchair's blacklist. Seeing
// one mid-sweep means the remaining batches are pointless.
var backoffStatuses = map[int]bool{
	http.StatusTooManyRequests: true,
	http.StatusTeapot:          true,
	430:                        true,
}

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
	batchSize := c.contractBatchSize()
	if c.apiKey == "" {
		if len(valid) > keylessMaxContractLookups {
			// #nosec G404 -- rotation of which public addresses get priced, not a security decision
			rand.Shuffle(len(valid), func(i, j int) { valid[i], valid[j] = valid[j], valid[i] })
			valid = valid[:keylessMaxContractLookups]
		}
	}
	result := make(map[string]*PriceData, len(valid))
	var errs []error

	for i := 0; i < len(valid); i += batchSize {
		// Batches are paced by the shared provider budget in the transport;
		// this loop only has to notice when the caller gave up.
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		end := min(i+batchSize, len(valid))
		batch := valid[i:end]

		// token_price supports market cap, volume and change — not high/low.
		// It used to be asked for include_24hr_high/low, which this endpoint
		// ignores without complaint: every contract-priced asset in the
		// catalogue ended up with high = low = 0, and none of them carried any
		// market context at all. A price with no volume behind it is what let
		// MNEP stand second in the dev portfolio (personal-6ae).
		url := fmt.Sprintf(
			"%s/simple/token_price/%s?contract_addresses=%s&vs_currencies=%s"+
				"&include_market_cap=true&include_24hr_vol=true&include_24hr_change=true",
			c.baseURL,
			platform,
			strings.Join(batch, ","),
			currency,
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return result, fmt.Errorf("create request: %w", err)
		}
		c.authenticate(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			errs = append(errs, fmt.Errorf("token_price batch %d-%d: %w", i, end, err))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			errs = append(errs, fmt.Errorf("token_price batch %d-%d: unexpected status %d from CoinGecko", i, end, resp.StatusCode))
			if backoffStatuses[resp.StatusCode] {
				// The budget is already spent: the transport has frozen this
				// credential, so every remaining batch would sit out the freeze
				// only to earn another 429 — twelve of them outlast the job
				// timeout and take the whole sweep down with them.
				break
			}
			continue
		}

		// Response: { "0x...": { "usd": 1.23, "usd_market_cap": 4e6, "usd_24h_vol": 1.2e5, "usd_24h_change": -3.4 }, ... }
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
				Price:         price,
				MarketCap:     prices[currency+"_market_cap"],
				Volume24h:     prices[currency+"_24h_vol"],
				ChangePercent: prices[currency+"_24h_change"],
				Timestamp:     now,
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
