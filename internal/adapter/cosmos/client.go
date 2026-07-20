// Package cosmos adapts the Cosmos SDK LCD REST API to entity.WalletSyncer,
// covering the native token of each supported zone including staked value.
package cosmos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

// ProviderName is the canonical provider slug for this adapter. The LCD API is
// public; an account still names the provider so the registry can route to it.
const ProviderName = "cosmos"

// defaultBaseURL is the community LCD proxy, which serves every chain under one
// scheme and fails over between public nodes.
const defaultBaseURL = "https://rest.cosmos.directory"

// Config holds LCD client configuration. BaseURL selects the endpoint and is
// the only knob: the API is public and unauthenticated.
type Config struct {
	BaseURL string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// Client talks to Cosmos SDK LCD REST endpoints.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an LCD client.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// Balance is an account's holding of one denom, decomposed by where the tokens
// sit.
//
// Unlike Substrate — where bonded tokens stay inside the free balance and
// adding them double-counts — these are genuinely separate pools. Delegated
// tokens have left the bank balance, unbonding ones are in escrow, and rewards
// are unclaimed but owned. All four have to be summed or the position reads far
// below what the wallet actually holds.
type Balance struct {
	Liquid    decimal.Decimal
	Delegated decimal.Decimal
	Unbonding decimal.Decimal
	Rewards   decimal.Decimal
}

// Total is everything the account owns of the denom.
func (b Balance) Total() decimal.Decimal {
	return b.Liquid.Add(b.Delegated).Add(b.Unbonding).Add(b.Rewards)
}

// GetBalance returns the full holding of a chain's native denom, summing all
// four pools. The address must already carry the chain's own prefix.
func (c *Client) GetBalance(ctx context.Context, chain, address string) (Balance, error) {
	net, ok := networks[chain]
	if !ok {
		return Balance{}, fmt.Errorf("unsupported chain %q", chain)
	}

	var balance Balance
	base := fmt.Sprintf("%s/%s", c.baseURL, net.lcdChain)

	// 1. Liquid: the bank balance.
	var bank struct {
		Balances []coin `json:"balances"`
	}
	if err := c.get(ctx, base+"/cosmos/bank/v1beta1/balances/"+address, &bank); err != nil {
		return Balance{}, fmt.Errorf("bank balances: %w", err)
	}
	balance.Liquid = sumCoins(bank.Balances, net.denom)

	// 2. Delegated: staked with validators, outside the bank balance.
	var staking struct {
		Responses []struct {
			Balance coin `json:"balance"`
		} `json:"delegation_responses"`
	}
	if err := c.get(ctx, base+"/cosmos/staking/v1beta1/delegations/"+address, &staking); err != nil {
		return Balance{}, fmt.Errorf("delegations: %w", err)
	}
	for _, r := range staking.Responses {
		balance.Delegated = balance.Delegated.Add(coinAmount(r.Balance, net.denom))
	}

	// 3. Unbonding: mid-unbond, in escrow until the period ends. Entries carry
	// a bare amount — the denom is implicitly the chain's bonded one.
	var unbonding struct {
		Responses []struct {
			Entries []struct {
				Balance string `json:"balance"`
			} `json:"entries"`
		} `json:"unbonding_responses"`
	}
	if err := c.get(ctx, base+"/cosmos/staking/v1beta1/delegators/"+address+"/unbonding_delegations", &unbonding); err != nil {
		return Balance{}, fmt.Errorf("unbonding delegations: %w", err)
	}
	for _, r := range unbonding.Responses {
		for _, e := range r.Entries {
			balance.Unbonding = balance.Unbonding.Add(parseAmount(e.Balance))
		}
	}

	// 4. Rewards: accrued but unclaimed. These are DecCoins — decimal strings
	// carrying 18 fractional digits — so the total is truncated back to whole
	// micro-units, the only resolution the chain actually settles.
	var distribution struct {
		Total []coin `json:"total"`
	}
	if err := c.get(ctx, base+"/cosmos/distribution/v1beta1/delegators/"+address+"/rewards", &distribution); err != nil {
		return Balance{}, fmt.Errorf("rewards: %w", err)
	}
	balance.Rewards = sumCoins(distribution.Total, net.denom).Truncate(0)

	return balance, nil
}

// coin is the {denom, amount} pair the SDK returns everywhere.
type coin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// sumCoins adds the entries matching denom. A wallet's bank balance also holds
// IBC vouchers of foreign tokens ("ibc/<hash>"), which are real value but need
// a denom trace to identify — out of scope here, so only the native denom is
// counted.
func sumCoins(coins []coin, denom string) decimal.Decimal {
	total := decimal.Zero
	for _, c := range coins {
		total = total.Add(coinAmount(c, denom))
	}
	return total
}

func coinAmount(c coin, denom string) decimal.Decimal {
	if c.Denom != denom {
		return decimal.Zero
	}
	return parseAmount(c.Amount)
}

// parseAmount reads an SDK amount. Absent fields mean "nothing here": the LCD
// omits pools an account does not use, and an account with no delegations has
// no entries at all.
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

func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// An address the chain has never seen is not an error: some LCD builds
	// answer 404 rather than an empty list, and both mean a zero balance.
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lcd status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
