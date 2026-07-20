// Package esplora adapts the Esplora address API (mempool.space, Blockstream)
// to entity.WalletSyncer, covering native BTC balances.
package esplora

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ProviderName is the canonical provider slug for this adapter. Esplora needs
// no credentials; an account still names the provider so the registry can route
// to it.
const ProviderName = "esplora"

// Chain is the single chain identifier this adapter serves.
const Chain = "bitcoin"

const (
	nativeSymbol   = "BTC"
	nativeName     = "Bitcoin"
	nativeDecimals = 8
)

// defaultBaseURL is mempool.space. Blockstream serves the same API shape at
// https://blockstream.info/api and works as a drop-in alternative.
const defaultBaseURL = "https://mempool.space/api"

// Config holds Esplora client configuration. BaseURL selects the instance and
// is the only knob: the API is public and unauthenticated.
type Config struct {
	BaseURL string
}

// Client talks to an Esplora REST instance.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an Esplora client.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetBalance returns the confirmed balance of an address in satoshis.
//
// Esplora reports lifetime totals rather than a balance, so the holding is
// funded minus spent. Only chain_stats is counted: mempool_stats covers
// unconfirmed activity, which can still be replaced or dropped and has no
// business moving a portfolio number.
func (c *Client) GetBalance(ctx context.Context, address string) (int64, error) {
	var parsed struct {
		ChainStats struct {
			// json.Number, not int64 directly: satoshi sums must not
			// round-trip through float64 on the way in.
			FundedSum json.Number `json:"funded_txo_sum"`
			SpentSum  json.Number `json:"spent_txo_sum"`
		} `json:"chain_stats"`
	}

	url := c.baseURL + "/address/" + address
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("esplora status %d for %s", resp.StatusCode, address)
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	funded, err := parseSats(parsed.ChainStats.FundedSum)
	if err != nil {
		return 0, fmt.Errorf("funded_txo_sum: %w", err)
	}
	spent, err := parseSats(parsed.ChainStats.SpentSum)
	if err != nil {
		return 0, fmt.Errorf("spent_txo_sum: %w", err)
	}
	return funded - spent, nil
}

// parseSats reads a satoshi total. An absent field means an address the chain
// has never seen, which is zero rather than an error; a malformed one is an
// error, since silently reading it as zero would erase a real balance.
func parseSats(n json.Number) (int64, error) {
	if n == "" {
		return 0, nil
	}
	return n.Int64()
}
