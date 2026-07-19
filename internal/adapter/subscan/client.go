// Package subscan adapts the Subscan multi-network API to entity.WalletSyncer,
// covering the Substrate chains (Polkadot, Kusama, Hydration, Astar, Moonbeam).
package subscan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

// ProviderName is the canonical provider slug for Subscan credentials.
const ProviderName = "subscan"

// Config holds Subscan client configuration.
type Config struct {
	APIKey string
}

// Client talks to the per-network Subscan REST API.
type Client struct {
	apiKey     string
	httpClient *http.Client

	// baseURLOverride replaces the per-network host when set (tests only).
	baseURLOverride string
}

// NewClient creates a Subscan client. A key is required for anything beyond
// trivial rate limits; the free tier is enough for periodic balance polling.
func NewClient(cfg Config) *Client {
	return &Client{
		apiKey:     cfg.APIKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Account holds the balance components Subscan reports for an address, in
// whole token units (not planck).
//
// Substrate's balance model matters here: an account's total is Free +
// Reserved. Locks (staking bonds, democracy, elections) are *restrictions on
// the free balance*, not separate pots — Bonded tokens are still counted in
// Free. Adding them to the total would double-count the largest position on a
// staking-heavy account, so they are kept for reporting only.
type Account struct {
	Free     decimal.Decimal
	Reserved decimal.Decimal

	// Bonded is staked (a subset of Free). Reported for observability, never
	// added to the total.
	Bonded decimal.Decimal
	// Unbonding is mid-unbond (also within Free).
	Unbonding decimal.Decimal
	// Locked is the largest active lock on Free (governance, vesting, staking).
	Locked decimal.Decimal
}

// Total is the account's full holding: free plus reserved. See the type doc
// for why locks and bonds are excluded.
func (a Account) Total() decimal.Decimal {
	return a.Free.Add(a.Reserved)
}

// searchResponse is the /api/v2/scan/search envelope. Subscan signals errors
// through code != 0 with HTTP 200, so the body must always be inspected.
type searchResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Account struct {
			Address   string `json:"address"`
			Balance   string `json:"balance"`
			Reserved  string `json:"reserved"`
			Bonded    string `json:"bonded"`
			Unbonding string `json:"unbonding"`
			Lock      string `json:"lock"`
		} `json:"account"`
	} `json:"data"`
}

// GetAccount fetches the balance breakdown of an address on one network.
// An address unknown to the chain is not an error: Subscan answers with an
// empty account, which maps to a zero balance.
func (c *Client) GetAccount(ctx context.Context, chain, address string) (Account, error) {
	net, ok := networks[chain]
	if !ok {
		return Account{}, fmt.Errorf("unsupported chain %q", chain)
	}

	url := fmt.Sprintf("https://%s.api.subscan.io/api/v2/scan/search", net.host)
	if c.baseURLOverride != "" {
		url = c.baseURLOverride + "/api/v2/scan/search"
	}

	body, err := json.Marshal(map[string]string{"key": address})
	if err != nil {
		return Account{}, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Account{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Account{}, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Account{}, fmt.Errorf("subscan API status %d for %s", resp.StatusCode, chain)
	}

	var parsed searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Account{}, fmt.Errorf("decode response: %w", err)
	}
	if parsed.Code != 0 {
		return Account{}, fmt.Errorf("subscan error for %s: code %d: %s", chain, parsed.Code, parsed.Message)
	}

	acc := parsed.Data.Account
	return Account{
		Free:      parseAmount(acc.Balance),
		Reserved:  parseAmount(acc.Reserved),
		Bonded:    parseAmount(acc.Bonded),
		Unbonding: parseAmount(acc.Unbonding),
		Locked:    parseAmount(acc.Lock),
	}, nil
}

// parseAmount reads a Subscan decimal string. Absent and unparsable fields
// both mean "nothing here": Subscan omits components an account does not use
// (a non-staking account has no bonded field at all), and a malformed number
// must not fail the whole sync of an otherwise valid account.
func parseAmount(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}
