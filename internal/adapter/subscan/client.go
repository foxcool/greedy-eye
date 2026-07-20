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

// Account holds the native balance Subscan reports for an address on one
// chain, as a raw integer in the chain's smallest unit (planck) together with
// the precision needed to read it.
//
// Balance is the account's ENTIRE holding on the chain. Reserved and Bonded
// are subsets of it, not additions: on Hydration the controller reports
// balance 34644.639967302550 HDX of which 34015.697423172136 is bonded, and on
// Kusama Asset Hub balance 6.016092032275 KSM is exactly transferable
// 0.394224319384 plus a staking hold of 5.621867712891. Adding any of them to
// Balance double-counts the largest position on a staking-heavy account.
//
// This replaced a Free + Reserved model read from /api/v2/scan/search, which
// was wrong twice over — see GetAccount for why that endpoint is unusable.
type Account struct {
	// Balance is raw, scaled by Decimals. It is never a whole-token figure.
	Balance decimal.Decimal
	// Decimals comes from the response, not from a table: it is the only
	// honest source for how to read Balance.
	Decimals int32
	// Symbol is the chain's native token as the API names it.
	Symbol string

	// Reserved, Bonded and Unbonding are subsets of Balance, kept for
	// observability and never added to it.
	Reserved  decimal.Decimal
	Bonded    decimal.Decimal
	Unbonding decimal.Decimal
}

// Total is the account's full holding. It is Balance alone — see the type doc.
func (a Account) Total() decimal.Decimal {
	return a.Balance
}

// tokensResponse is the /api/scan/account/tokens envelope. Subscan signals
// errors through code != 0 with HTTP 200, so the body must always be inspected.
//
// Only the native token is read here. The builtin/assets/erc20 arrays that
// this endpoint also returns carry the ecosystem's non-native holdings (USDT
// on Hydration, DED and MYTH on Polkadot Asset Hub) and are the subject of
// personal-feb.10 — the fixtures in this package deliberately keep them so the
// parser's indifference to them is covered by tests.
type tokensResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		// Native is null for an address the chain has never seen, which is
		// not an error: it maps to a zero balance.
		Native []struct {
			Symbol    string `json:"symbol"`
			Decimals  int32  `json:"decimals"`
			Balance   string `json:"balance"`
			Reserved  string `json:"reserved"`
			Bonded    string `json:"bonded"`
			Unbonding string `json:"unbonding"`
		} `json:"native"`
	} `json:"data"`
}

// GetAccount fetches the native balance of an address on one network.
// An address unknown to the chain is not an error: Subscan answers with a null
// native array, which maps to a zero balance.
//
// This reads /api/scan/account/tokens rather than /api/v2/scan/search, which
// cannot be parsed safely at all. That endpoint mixes units inside a single
// object — on Kusama Asset Hub `balance` and `lock` come as whole tokens
// ("6.016092032275") while `reserved`, `bonded` and `transferable_balance`
// come as planck ("5621867712891") — and it carries no decimals field to tell
// them apart. Reading it produced a holding of 5.62 trillion KSM against a
// total supply of 15 million. The tokens endpoint reports every field as raw
// planck and states its own precision.
func (c *Client) GetAccount(ctx context.Context, chain, address string) (Account, error) {
	net, ok := networks[chain]
	if !ok {
		return Account{}, fmt.Errorf("unsupported chain %q", chain)
	}

	url := fmt.Sprintf("https://%s.api.subscan.io/api/scan/account/tokens", net.host)
	if c.baseURLOverride != "" {
		url = c.baseURLOverride + "/api/scan/account/tokens"
	}

	body, err := json.Marshal(map[string]string{"address": address})
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

	var parsed tokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Account{}, fmt.Errorf("decode response: %w", err)
	}
	if parsed.Code != 0 {
		return Account{}, fmt.Errorf("subscan error for %s: code %d: %s", chain, parsed.Code, parsed.Message)
	}

	// A null or empty native array is an address the chain has never seen.
	// Reported with the chain's own precision so the caller cannot mistake a
	// zero for a decimals-less amount.
	if len(parsed.Data.Native) == 0 {
		return Account{Symbol: net.symbol, Decimals: net.decimals}, nil
	}

	native := parsed.Data.Native[0]

	// Decimals must come from the response. Falling back to the table would
	// reintroduce exactly the guess this endpoint exists to remove, so a
	// missing precision is an error rather than a silently wrong scale.
	if native.Decimals <= 0 {
		return Account{}, fmt.Errorf("subscan %s: native balance without decimals", chain)
	}

	balance, err := parseAmount(chain, "balance", native.Balance)
	if err != nil {
		return Account{}, err
	}

	symbol := native.Symbol
	if symbol == "" {
		symbol = net.symbol
	}

	return Account{
		Balance:  balance,
		Decimals: native.Decimals,
		Symbol:   symbol,
		// Subsets of Balance; a malformed one must not fail an otherwise
		// valid sync, so these degrade to zero.
		Reserved:  optionalAmount(native.Reserved),
		Bonded:    optionalAmount(native.Bonded),
		Unbonding: optionalAmount(native.Unbonding),
	}, nil
}

// parseAmount reads a raw planck integer that the holding depends on. An empty
// field means zero — Subscan omits components an account does not use — but
// a non-empty field that will not parse is fatal: returning zero there would
// report an account as empty when it is not, which is the failure mode that
// cannot be noticed downstream.
func parseAmount(chain, field, s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("subscan %s: unparsable %s %q", chain, field, s)
	}
	return d, nil
}

// optionalAmount reads a field kept only for observability. These never reach
// the holding, so a malformed one degrades to zero rather than failing a sync
// whose balance parsed fine.
func optionalAmount(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}
