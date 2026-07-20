// Package tonapi adapts the tonapi.io REST API to entity.WalletSyncer,
// covering native TON and jetton balances.
package tonapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ProviderName is the canonical provider slug for tonapi credentials.
const ProviderName = "tonapi"

// Chain is the single chain identifier this adapter serves.
const Chain = "ton"

const (
	nativeSymbol   = "TON"
	nativeName     = "Toncoin"
	nativeDecimals = 9
)

// Config holds tonapi client configuration. The API serves anonymous requests
// at a lower rate limit, so the key is optional.
type Config struct {
	APIKey string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// Client talks to the tonapi.io v2 REST API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a tonapi client.
func NewClient(cfg Config) *Client {
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    "https://tonapi.io",
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// Jetton is one jetton balance held by an account.
type Jetton struct {
	Symbol   string
	Name     string
	Address  string // jetton master contract
	Decimals int
	// Amount is the raw integer balance scaled by Decimals.
	Amount string
	// Verification is tonapi's curation state: whitelist, none or blacklist.
	Verification string
	// IsScam flags the holder's jetton wallet, not the jetton itself.
	IsScam bool
}

// GetAccountBalance returns the native TON balance in nanotons.
func (c *Client) GetAccountBalance(ctx context.Context, address string) (string, error) {
	var parsed struct {
		// Decoded as json.Number, not int64: the balance is raw nanotons and
		// must not round-trip through float64.
		Balance json.Number `json:"balance"`
		Status  string      `json:"status"`
	}
	if err := c.get(ctx, "/v2/accounts/"+address, &parsed); err != nil {
		return "", err
	}
	if parsed.Balance == "" {
		return "0", nil
	}
	return parsed.Balance.String(), nil
}

// GetJettons returns the jetton balances of an account, including liquid
// staking positions such as tsTON, which are ordinary jettons on TON.
func (c *Client) GetJettons(ctx context.Context, address string) ([]Jetton, error) {
	var parsed struct {
		Balances []struct {
			Balance       string `json:"balance"`
			WalletAddress struct {
				IsScam bool `json:"is_scam"`
			} `json:"wallet_address"`
			Jetton struct {
				Address      string `json:"address"`
				Name         string `json:"name"`
				Symbol       string `json:"symbol"`
				Decimals     int    `json:"decimals"`
				Verification string `json:"verification"`
			} `json:"jetton"`
		} `json:"balances"`
	}
	if err := c.get(ctx, "/v2/accounts/"+address+"/jettons", &parsed); err != nil {
		return nil, err
	}

	jettons := make([]Jetton, 0, len(parsed.Balances))
	for _, b := range parsed.Balances {
		jettons = append(jettons, Jetton{
			Symbol:       b.Jetton.Symbol,
			Name:         b.Jetton.Name,
			Address:      b.Jetton.Address,
			Decimals:     b.Jetton.Decimals,
			Amount:       b.Balance,
			Verification: b.Jetton.Verification,
			IsScam:       b.WalletAddress.IsScam,
		})
	}
	return jettons, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tonapi status %d for %s", resp.StatusCode, path)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
