// Package tzkt adapts the TzKT REST API to entity.WalletSyncer, covering
// native XTZ including staked value.
package tzkt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

// ProviderName is the canonical provider slug for this adapter. TzKT is public;
// an account still names the provider so the registry can route to it.
const ProviderName = "tzkt"

// Chain is the single chain identifier this adapter serves.
const Chain = "tezos"

const (
	nativeSymbol   = "XTZ"
	nativeName     = "Tezos"
	nativeDecimals = 6
)

const defaultBaseURL = "https://api.tzkt.io"

// Config holds TzKT client configuration. BaseURL selects the instance and is
// the only knob: the API is public and unauthenticated.
type Config struct {
	BaseURL string
}

// Client talks to the TzKT REST API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a TzKT client.
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

// Account holds the balance components TzKT reports for an address, in mutez.
//
// Balance is already the whole holding: TzKT documents it as spendable plus
// frozen funds, and staking under Paris freezes tokens in place rather than
// moving them out. Verified against live accounts — on a baker,
// balance - stakedBalance came out exactly equal to ownDelegatedBalance, and on
// accounts mid-unstake, unstakedBalance accounted for nearly all of balance.
//
// So Staked and Unstaked are subsets, kept for observability and never added.
// Summing them would have roughly doubled a staking-heavy position, which is
// the same trap the Substrate adapter documents for bonded balances.
type Account struct {
	Balance decimal.Decimal

	// Staked is frozen with a baker (a subset of Balance).
	Staked decimal.Decimal
	// Unstaked is unstaked but not yet finalized, so still frozen (also within
	// Balance).
	Unstaked decimal.Decimal
}

// Total is everything the account owns. See the type doc for why the staking
// fields are excluded.
func (a Account) Total() decimal.Decimal {
	return a.Balance
}

// GetAccount fetches the balance breakdown of an address. An address unknown to
// the chain is not an error: TzKT answers with a type of "empty" and zeroes.
func (c *Client) GetAccount(ctx context.Context, address string) (Account, error) {
	var parsed struct {
		// json.Number throughout: mutez are raw integers and must not
		// round-trip through float64.
		Balance         json.Number `json:"balance"`
		StakedBalance   json.Number `json:"stakedBalance"`
		UnstakedBalance json.Number `json:"unstakedBalance"`
	}

	url := c.baseURL + "/v1/accounts/" + address
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Account{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Account{}, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Account{}, fmt.Errorf("tzkt status %d for %s", resp.StatusCode, address)
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Account{}, fmt.Errorf("decode response: %w", err)
	}

	return Account{
		Balance:  parseAmount(parsed.Balance),
		Staked:   parseAmount(parsed.StakedBalance),
		Unstaked: parseAmount(parsed.UnstakedBalance),
	}, nil
}

// parseAmount reads a mutez field. Absent and unparsable both mean "nothing
// here": TzKT omits the staking fields for accounts that never staked, and a
// malformed number must not fail the sync of an otherwise valid account.
func parseAmount(n json.Number) decimal.Decimal {
	if n == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil {
		return decimal.Zero
	}
	return d
}
