package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client implements ExchangeClient interface for Binance
type Client struct {
	apiKey     string
	apiSecret  string
	baseURL    string
	sandbox    bool
	httpClient *http.Client
}

// TickerPrice is the price data returned by GET /api/v3/ticker/price.
type TickerPrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"` // Binance returns price as decimal string
}

// Config holds Binance client configuration
type Config struct {
	APIKey    string
	APISecret string
	Sandbox   bool

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// Balance represents account balance for an asset
type Balance struct {
	Asset  string
	Free   float64
	Locked float64
}

// Order represents a trading order
type Order struct {
	OrderID     string
	Symbol      string
	Side        string // BUY, SELL
	Type        string // MARKET, LIMIT
	Price       float64
	Quantity    float64
	ExecutedQty float64
	Status      string
	TimeInForce string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Trade represents a completed trade
type Trade struct {
	TradeID   string
	OrderID   string
	Symbol    string
	Side      string
	Price     float64
	Quantity  float64
	Fee       float64
	FeeAsset  string
	Timestamp time.Time
}

// NewClient creates a new Binance exchange client
func NewClient(cfg Config) *Client {
	baseURL := "https://api.binance.com"
	if cfg.Sandbox {
		baseURL = "https://testnet.binance.vision"
	}

	return &Client{
		apiKey:     cfg.APIKey,
		apiSecret:  cfg.APISecret,
		baseURL:    baseURL,
		sandbox:    cfg.Sandbox,
		httpClient: &http.Client{Timeout: 10 * time.Second, Transport: cfg.Transport},
	}
}

// WithBaseURL overrides the API base URL. Intended for tests (httptest server).
func (c *Client) WithBaseURL(baseURL string) *Client {
	c.baseURL = baseURL
	return c
}

// GetTickerPrices fetches current prices for the given symbols.
// Uses the public GET /api/v3/ticker/price endpoint — no auth required.
// Pass multiple symbols as a JSON array: symbols=["BTCUSDT","ETHUSDT"].
func (c *Client) GetTickerPrices(ctx context.Context, symbols []string) ([]TickerPrice, error) {
	if len(symbols) == 0 {
		return nil, nil
	}

	encoded, err := json.Marshal(symbols)
	if err != nil {
		return nil, fmt.Errorf("encode symbols: %w", err)
	}

	url := c.baseURL + "/api/v3/ticker/price?symbols=" + strings.ReplaceAll(string(encoded), " ", "")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from Binance", resp.StatusCode)
	}

	var tickers []TickerPrice
	if err := json.NewDecoder(resp.Body).Decode(&tickers); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return tickers, nil
}

// rawBalance is a single balance from GET /api/v3/account, amounts as decimal
// strings (kept exact — floats would lose precision on large crypto amounts).
type rawBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

// accountResponse is the subset of GET /api/v3/account we consume.
type accountResponse struct {
	Balances []rawBalance `json:"balances"`
}

// binanceError is the {"code":-1121,"msg":"..."} shape Binance returns on 4xx.
type binanceError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// signedGet performs a SIGNED Binance request: it appends timestamp+recvWindow,
// signs the query with the API secret (HMAC-SHA256), and sets the API key header.
func (c *Client) signedGet(ctx context.Context, path string, params url.Values) (*http.Response, error) {
	if c.apiKey == "" || c.apiSecret == "" {
		return nil, status.Error(codes.FailedPrecondition, "binance: api key and secret are required for signed endpoints")
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("recvWindow", "5000")

	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(params.Encode()))
	params.Set("signature", hex.EncodeToString(mac.Sum(nil)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	return c.httpClient.Do(req)
}

// fetchAccount calls the SIGNED GET /api/v3/account endpoint and returns the
// raw (string-amount) balances, preserving exact precision.
func (c *Client) fetchAccount(ctx context.Context) ([]rawBalance, error) {
	resp, err := c.signedGet(ctx, "/api/v3/account", nil)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var be binanceError
		if json.NewDecoder(resp.Body).Decode(&be) == nil && be.Msg != "" {
			return nil, fmt.Errorf("binance account: status %d: %s (code %d)", resp.StatusCode, be.Msg, be.Code)
		}
		return nil, fmt.Errorf("binance account: unexpected status %d", resp.StatusCode)
	}

	var account accountResponse
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return account.Balances, nil
}

// GetAccountBalances retrieves all non-zero spot account balances.
// Uses the SIGNED GET /api/v3/account endpoint. accountID is unused: the API
// key identifies the account. Amounts are float64 for display/trading use; the
// balance syncer path preserves exact precision separately.
func (c *Client) GetAccountBalances(ctx context.Context, _ string) ([]Balance, error) {
	raw, err := c.fetchAccount(ctx)
	if err != nil {
		return nil, err
	}
	balances := make([]Balance, 0, len(raw))
	for _, b := range raw {
		free, err := strconv.ParseFloat(b.Free, 64)
		if err != nil {
			return nil, fmt.Errorf("parse free balance for %s: %w", b.Asset, err)
		}
		locked, err := strconv.ParseFloat(b.Locked, 64)
		if err != nil {
			return nil, fmt.Errorf("parse locked balance for %s: %w", b.Asset, err)
		}
		if free == 0 && locked == 0 {
			continue
		}
		balances = append(balances, Balance{Asset: b.Asset, Free: free, Locked: locked})
	}
	return balances, nil
}

// GetAssetBalance retrieves balance for a specific asset
func (c *Client) GetAssetBalance(ctx context.Context, accountID string, asset string) (*Balance, error) {
	return nil, status.Error(codes.Unimplemented, "GetAssetBalance not implemented")
}

// PlaceOrder creates a new order
func (c *Client) PlaceOrder(ctx context.Context, accountID string, order *Order) (*Order, error) {
	return nil, status.Error(codes.Unimplemented, "PlaceOrder not implemented")
}

// CancelOrder cancels an existing order
func (c *Client) CancelOrder(ctx context.Context, accountID string, orderID string, symbol string) error {
	return status.Error(codes.Unimplemented, "CancelOrder not implemented")
}

// GetOrder retrieves order details
func (c *Client) GetOrder(ctx context.Context, accountID string, orderID string, symbol string) (*Order, error) {
	return nil, status.Error(codes.Unimplemented, "GetOrder not implemented")
}

// GetOpenOrders retrieves all open orders
func (c *Client) GetOpenOrders(ctx context.Context, accountID string, symbol string) ([]Order, error) {
	return nil, status.Error(codes.Unimplemented, "GetOpenOrders not implemented")
}

// GetOrderHistory retrieves order history
func (c *Client) GetOrderHistory(ctx context.Context, accountID string, symbol string, limit int) ([]Order, error) {
	return nil, status.Error(codes.Unimplemented, "GetOrderHistory not implemented")
}

// GetTradeHistory retrieves trade history
func (c *Client) GetTradeHistory(ctx context.Context, accountID string, symbol string, limit int) ([]Trade, error) {
	return nil, status.Error(codes.Unimplemented, "GetTradeHistory not implemented")
}

// GetSymbolPrice retrieves current price for a trading pair
func (c *Client) GetSymbolPrice(ctx context.Context, symbol string) (float64, error) {
	return 0, status.Error(codes.Unimplemented, "GetSymbolPrice not implemented")
}

// ValidateAccount verifies account credentials and permissions
func (c *Client) ValidateAccount(ctx context.Context, accountID string) error {
	return status.Error(codes.Unimplemented, "ValidateAccount not implemented")
}
