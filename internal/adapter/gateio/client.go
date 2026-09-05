// Package gateio reads spot balances from a Gate.io account. Balances only —
// pricing the venue is personal-nzir, and needs the pair binding rather than
// another endpoint.
package gateio

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	// ProviderName is the canonical source identifier for Gate.io.
	ProviderName = "gateio"

	defaultBaseURL = "https://api.gateio.ws"
	accountsPath   = "/api/v4/spot/accounts"
)

// Client talks to the Gate.io v4 REST API on behalf of one account.
type Client struct {
	apiKey     string
	apiSecret  string
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

// Config holds what one account's client needs.
type Config struct {
	APIKey    string
	APISecret string

	// BaseURL overrides the API host: a regional host, or a test server.
	BaseURL string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// NewClient builds a client for one account's credentials.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:     cfg.APIKey,
		apiSecret:  cfg.APISecret,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second, Transport: cfg.Transport},
		now:        time.Now,
	}
}

// spotAccount is one currency's spot balance as Gate.io reports it. Amounts
// arrive as decimal strings in whole units, not as scaled integers.
type spotAccount struct {
	Currency  string `json:"currency"`
	Available string `json:"available"`
	Locked    string `json:"locked"`
}

// apiError is Gate.io's error envelope, read only to name the reason: the
// status code already says that something failed.
type apiError struct {
	Label   string `json:"label"`
	Message string `json:"message"`
}

// sign builds the v4 signature headers: method, path, query, the hex SHA-512 of
// the body, and the timestamp, newline-separated.
//
// The body hash is there even with no body — for a GET it is the hash of the
// empty string, not an empty field. Omitting it shifts every later line up one
// and the API refuses with a label carrying no detail.
func (c *Client) sign(method, path, query string, body []byte) http.Header {
	timestamp := strconv.FormatInt(c.now().Unix(), 10)

	bodyHash := sha512.Sum512(body)
	payload := method + "\n" + path + "\n" + query + "\n" + hex.EncodeToString(bodyHash[:]) + "\n" + timestamp

	mac := hmac.New(sha512.New, []byte(c.apiSecret))
	mac.Write([]byte(payload))

	h := http.Header{}
	h.Set("KEY", c.apiKey)
	h.Set("SIGN", hex.EncodeToString(mac.Sum(nil)))
	h.Set("Timestamp", timestamp)
	h.Set("Accept", "application/json")
	return h
}

// fetchSpotAccounts calls the SIGNED GET /api/v4/spot/accounts, returning the
// raw strings so precision is the caller's decision rather than lost here.
func (c *Client) fetchSpotAccounts(ctx context.Context) ([]spotAccount, error) {
	if c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("gateio: api key and secret are required for signed endpoints")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+accountsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = c.sign(http.MethodGet, accountsPath, "", nil)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var e apiError
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Label != "" {
			return nil, fmt.Errorf("gateio spot accounts: status %d: %s (%s)", resp.StatusCode, e.Message, e.Label)
		}
		return nil, fmt.Errorf("gateio spot accounts: unexpected status %d", resp.StatusCode)
	}

	var accounts []spotAccount
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return accounts, nil
}
