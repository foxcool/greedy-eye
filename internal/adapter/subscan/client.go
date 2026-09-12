// Package subscan adapts the Subscan multi-network API to entity.WalletSyncer,
// covering the Substrate chains (Polkadot, Kusama, the Asset Hubs, Hydration,
// Astar, Moonbeam) — the chain's own coin and the assets kept beside it.
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

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// Client talks to the per-network Subscan REST API.
type Client struct {
	apiKey     string
	httpClient *http.Client

	// baseURLOverride replaces the per-network host when set (tests only).
	baseURLOverride string
}

// NewClient creates a Subscan client. A key is mandatory: as of 2026-08-02 an
// unauthenticated request is answered "Subscan API strictly requires an API key.
// Unauthenticated access is disabled", whatever the endpoint.
func NewClient(cfg Config) *Client {
	return &Client{
		apiKey:     cfg.APIKey,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
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

	// Reserved, Bonded and Unbonding are subsets of Balance, never added to it.
	// They are also not disjoint from EACH OTHER: measured on 2026-08-02, Kusama
	// Asset Hub reports the same 5.637369256383 KSM as reserved, as bonded and
	// as lock against a balance of 6.031593575767. splitLiquidity reconciles
	// them before partitioning and declines to split when it cannot.
	Reserved  decimal.Decimal
	Bonded    decimal.Decimal
	Unbonding decimal.Decimal

	// Tokens are the non-native holdings reported beside the chain's own coin.
	// Empty on a relay chain, which carries nothing else.
	Tokens []Token

	// Skipped names the token entries this chain reported and the parser
	// refused, one string each. They ride beside the balances rather than
	// inside an error because they are not a failure of the sync: the chain
	// answered, and everything else it said is usable. Dropping a token
	// silently is the one option not on the table — a position nobody can see
	// is indistinguishable from a position nobody holds.
	Skipped []string
}

// Token is one non-native holding on a chain: an entry of the tokens endpoint's
// builtin or assets array.
//
// Asset registration on the Asset Hubs is permissionless, exactly as an ERC-20
// deployment is. Symbol and name are therefore claims by whoever registered the
// asset, and UniqueID is the only part of this the chain itself assigns.
type Token struct {
	// Symbol is what the registrant called it. Two assets may claim one ticker.
	Symbol string
	// Decimals comes from the entry. A token whose entry omits it is refused
	// rather than read as whole units: the gap between 0 and 18 is the
	// difference between a position and a lie about one (personal-feb.12).
	Decimals int32
	// Balance is raw, scaled by Decimals.
	Balance decimal.Decimal
	// Lock is the frozen part of Balance where the entry reports one. It is a
	// subset of Balance, never an addition — same model as the native coin.
	Lock decimal.Decimal
	// UniqueID is Subscan's identity for this asset ON THIS CHAIN
	// ("standard_assets/30", "standard_foreign_assets/6212dc…"). It is what an
	// asset_external_ref is keyed by, so a second asset claiming the same
	// ticker lands beside this one instead of on top of it.
	UniqueID string
}

// Total is the account's full holding of the NATIVE coin. It is Balance
// alone — see the type doc. Tokens are separate positions, never part of it.
func (a Account) Total() decimal.Decimal {
	return a.Balance
}

// tokensResponse is the /api/scan/account/tokens envelope. Subscan signals
// errors through code != 0 with HTTP 200, so the body must always be inspected.
//
// The response groups holdings by where the chain keeps them. Native is the
// chain's own coin. Builtin is a chain's multi-token pallet (Hydration keeps
// USDT there) and assets is pallet-assets on the Asset Hubs, where DED sits at
// id 30 and MYTH arrives teleported from parachain 3369. Both carry the same
// fields and are read the same way.
//
// erc20 is NOT read. It is served for the EVM-addressed chains (Moonbeam,
// Astar's H160 side), no account here reaches one, and no response captured
// from this endpoint has ever contained the array — so its field names would
// be guessed rather than measured, which is how a position gets silently
// rescaled. It is personal-feb.15, with the prior question attached: an ERC-20
// on an EVM-compatible parachain is also readable by an EVM balance reader,
// and two readers of one balance is how ETH counted twice on Optimism
// (personal-b1o9).
type tokensResponse struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    tokensData `json:"data"`
}

// tokensData is the payload's groups.
type tokensData struct {
	// Native is null for an address the chain has never seen, which is
	// not an error: it maps to a zero balance.
	Native []nativeEntry `json:"native"`

	Builtin []tokenEntry `json:"builtin"`
	Assets  []tokenEntry `json:"assets"`
}

// nativeEntry is the chain's own coin.
type nativeEntry struct {
	Symbol    string `json:"symbol"`
	Decimals  int32  `json:"decimals"`
	Balance   string `json:"balance"`
	Reserved  string `json:"reserved"`
	Bonded    string `json:"bonded"`
	Unbonding string `json:"unbonding"`
}

// tokenEntry is one non-native holding as the API reports it.
//
// Decimals is a POINTER because absent and zero must not be the same value
// here. Zero decimals is legitimate — an asset can be denominated in whole
// units — while an absent field means the entry does not say how to read its
// own number, and reading it as whole units would inflate an 18-decimal
// position by 10^18. The one case the native parser guards with `<= 0` needs
// three states at this level, not two.
type tokenEntry struct {
	Symbol   string `json:"symbol"`
	UniqueID string `json:"unique_id"`
	Decimals *int32 `json:"decimals"`
	Balance  string `json:"balance"`
	Lock     string `json:"lock"`
}

// tokens reads the non-native groups into positions, and names what it refused.
//
// Refusals are per ENTRY, never per chain. An Asset Hub accepts a registration
// from anyone, so one malformed entry is a thing a stranger can put in this
// account's response — failing the chain on it would let that stranger stop
// DOT from syncing.
//
// A repeated unique_id is refused for the same reason it would be expensive:
// downstream, holdings merge per (asset, chain), so two rows of one identity
// are summed rather than shown twice, and a doubled position states a number
// nobody holds.
func (d tokensData) tokens(chain string) ([]Token, []string) {
	var (
		out     []Token
		skipped []string
		seen    = make(map[string]bool)
	)
	for _, group := range [][]tokenEntry{d.Builtin, d.Assets} {
		for _, e := range group {
			id := e.UniqueID
			if id == "" {
				skipped = append(skipped, fmt.Sprintf("%s: token %q has no unique_id", chain, e.Symbol))
				continue
			}
			if seen[id] {
				skipped = append(skipped, fmt.Sprintf("%s: token %s reported twice under %s", chain, e.Symbol, id))
				continue
			}
			if e.Decimals == nil {
				skipped = append(skipped, fmt.Sprintf("%s: token %s (%s) reports no decimals", chain, e.Symbol, id))
				continue
			}
			balance, err := parseAmount(chain, "token balance", e.Balance)
			if err != nil {
				skipped = append(skipped, err.Error())
				continue
			}
			seen[id] = true
			if balance.IsZero() {
				// An asset the account has held and spent stays in the
				// response at zero. It is not a position.
				continue
			}
			out = append(out, Token{
				Symbol:   e.Symbol,
				Decimals: *e.Decimals,
				Balance:  balance,
				Lock:     optionalAmount(e.Lock),
				UniqueID: id,
			})
		}
	}
	return out, skipped
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

	tokens, skipped := parsed.Data.tokens(chain)

	// A null or empty native array is an address the chain has never seen.
	// Reported with the chain's own precision so the caller cannot mistake a
	// zero for a decimals-less amount.
	//
	// Tokens still ride along: an account can hold a pallet asset with none of
	// the chain's own coin left, and "no native balance" must not be read as
	// "nothing here" (the Asset Hubs are where that is most likely).
	if len(parsed.Data.Native) == 0 {
		return Account{Symbol: net.symbol, Decimals: net.decimals, Tokens: tokens, Skipped: skipped}, nil
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
		Tokens:    tokens,
		Skipped:   skipped,
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
