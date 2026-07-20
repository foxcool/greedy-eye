// Package blockchair adapts the Blockchair dashboards API to
// entity.WalletSyncer, covering the smaller UTXO chains under one client.
package blockchair

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ProviderName is the canonical provider slug for Blockchair credentials.
const ProviderName = "blockchair"

// defaultBaseURL is the public API root.
const defaultBaseURL = "https://api.blockchair.com"

// Config holds Blockchair client configuration.
//
// The key is nominally optional, but the free tier is rate-limited per IP and
// answers 430 ("temporarily blacklisted") once a shared address has spent the
// budget — which it already does from some networks. Treat keyless operation as
// best-effort.
type Config struct {
	APIKey  string
	BaseURL string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// Client talks to the Blockchair dashboards API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Blockchair client.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// GetBalance returns the balance of an address in the chain's smallest unit.
// An address the chain has never seen is not an error: Blockchair answers with
// a null entry, which maps to a zero balance.
func (c *Client) GetBalance(ctx context.Context, chain, address string) (int64, error) {
	net, ok := networks[chain]
	if !ok {
		return 0, fmt.Errorf("unsupported chain %q", chain)
	}

	endpoint := fmt.Sprintf("%s/%s/dashboards/address/%s", c.baseURL, net.path, url.PathEscape(address))
	if c.apiKey != "" {
		endpoint += "?key=" + url.QueryEscape(c.apiKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Blockchair puts its own status in the body alongside the HTTP one, and
	// the body carries the reason — rate limiting in particular arrives as 430
	// with an explanation worth surfacing.
	var parsed struct {
		Data map[string]struct {
			Address *struct {
				// json.Number, not int64: the balance is a raw integer and
				// must not round-trip through float64.
				Balance json.Number `json:"balance"`
			} `json:"address"`
		} `json:"data"`
		Context struct {
			Code  int    `json:"code"`
			Error string `json:"error"`
		} `json:"context"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("blockchair status %d for %s", resp.StatusCode, chain)
		}
		return 0, fmt.Errorf("decode response: %w", err)
	}
	if parsed.Context.Error != "" {
		return 0, fmt.Errorf("blockchair error for %s: code %d: %s",
			chain, parsed.Context.Code, parsed.Context.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("blockchair status %d for %s", resp.StatusCode, chain)
	}

	entry, ok := parsed.Data[address]
	if !ok || entry.Address == nil || entry.Address.Balance == "" {
		return 0, nil // never seen on this chain
	}
	return entry.Address.Balance.Int64()
}
