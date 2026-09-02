// Package alchemy reads EVM balances through Alchemy's Portfolio API.
//
// It exists because Moralis, until 2026-09-01 the only EVM balance source in
// this build, ended its free tier — the account kept its keys and lost its
// entitlement. An outage is waited out; a withdrawn tier is replaced. What that
// removed was not a degraded provider but a whole leg of the inventory: eleven
// chains no other adapter here can read.
package alchemy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProviderName is the canonical provider slug for Alchemy credentials.
const ProviderName = "alchemy"

// maxNetworksPerCall is Alchemy's cap on networks per address in one request.
// Exceeding it is rejected outright, so the chain set is asked for in chunks.
const maxNetworksPerCall = 5

// maxPages bounds pagination. A wallet holding more tokens than this is a
// wallet whose tail we would be silently truncating, and truncation is the
// failure this project keeps paying for — so the walk stops and says so
// instead of returning a prefix that looks complete.
const maxPages = 20

// Client talks to the Portfolio API.
//
// One request carries the native coin AND the ERC-20s for up to five networks,
// which is why this adapter asks for balances per address rather than per
// chain: the previous source needed two calls per chain, this needs one per
// five.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Config holds Alchemy client configuration.
type Config struct {
	APIKey string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// NewClient creates a new Alchemy client.
func NewClient(cfg Config) *Client {
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    "https://api.g.alchemy.com/data/v1",
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// Token is one balance as the Portfolio API reports it. Native coins carry an
// empty TokenAddress; the API sends null there and that absence is the only
// thing distinguishing a chain's own coin from a contract on it.
type Token struct {
	Network      string
	Address      string
	TokenAddress string
	// TokenBalance is a hex quantity string ("0x02c6..."), not a decimal one.
	TokenBalance string
	Name         string
	Symbol       string
	Decimals     *int
	// Err is the API's per-token failure: metadata or pricing that could not
	// be fetched for this row while the network answered fine. Distinct from a
	// network-level failure, and reported separately for the same reason the
	// sync counts skipped positions rather than dropping them.
	Err string
}

// tokensRequest is the Portfolio API request body.
//
// withPrices is false and must stay false. Alchemy will happily quote every
// balance it returns, and a balance source that also prices is a second author
// of the total — the way the frontend once was. Prices belong to the price
// path, which dates them, gates them by market depth and records where they
// came from; none of which a number smuggled in beside a balance would get.
type tokensRequest struct {
	Addresses []addressNetworks `json:"addresses"`

	WithMetadata        bool   `json:"withMetadata"`
	WithPrices          bool   `json:"withPrices"`
	IncludeNativeTokens bool   `json:"includeNativeTokens"`
	IncludeErc20Tokens  bool   `json:"includeErc20Tokens"`
	PageKey             string `json:"pageKey,omitempty"`
}

type addressNetworks struct {
	Address  string   `json:"address"`
	Networks []string `json:"networks"`
}

type tokensResponse struct {
	Data struct {
		Tokens []struct {
			Address       string  `json:"address"`
			Network       string  `json:"network"`
			TokenAddress  *string `json:"tokenAddress"`
			TokenBalance  string  `json:"tokenBalance"`
			TokenMetadata *struct {
				Decimals *int    `json:"decimals"`
				Name     *string `json:"name"`
				Symbol   *string `json:"symbol"`
			} `json:"tokenMetadata"`
			Error *string `json:"error"`
		} `json:"tokens"`
		PageKey string `json:"pageKey"`
	} `json:"data"`
	// Error reports networks that failed as a whole. It is absent when every
	// network answered, so its presence is the signal — not its contents.
	Error *struct {
		Message       string `json:"message"`
		PartialErrors []struct {
			Network string `json:"network"`
			Message string `json:"message"`
		} `json:"partialErrors"`
	} `json:"error"`
}

// NetworkError names a network that failed while others answered. It is a
// value rather than an error so a partial result stays usable: the caller
// returns what it got AND says what is missing from it.
type NetworkError struct {
	Network string
	Message string
}

// TokensByAddress returns every native and ERC-20 balance the given networks
// report for one address, following pagination to the end.
//
// A network that fails comes back in the second return value rather than
// failing the call: the wallet's other chains are still true, and a sum that
// silently loses a chain is the failure this whole path exists to prevent. An
// error is returned only when nothing could be read at all.
func (c *Client) TokensByAddress(ctx context.Context, address string, networks []string) ([]Token, []NetworkError, error) {
	if len(networks) == 0 {
		return nil, nil, nil
	}
	if len(networks) > maxNetworksPerCall {
		return nil, nil, fmt.Errorf("alchemy: %d networks in one call, the API accepts %d", len(networks), maxNetworksPerCall)
	}

	var (
		tokens   []Token
		netErrs  []NetworkError
		pageKey  string
		pageSeen int
	)
	for {
		body, err := c.postTokens(ctx, tokensRequest{
			Addresses:           []addressNetworks{{Address: address, Networks: networks}},
			WithMetadata:        true,
			WithPrices:          false,
			IncludeNativeTokens: true,
			IncludeErc20Tokens:  true,
			PageKey:             pageKey,
		})
		if err != nil {
			return nil, nil, err
		}

		var parsed tokensResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, nil, fmt.Errorf("alchemy: decode tokens: %w", err)
		}

		// Network failures repeat on every page; recording them once keeps the
		// report about the network rather than about how long the walk was.
		if pageSeen == 0 && parsed.Error != nil {
			netErrs = append(netErrs, networkErrors(parsed)...)
		}

		for _, t := range parsed.Data.Tokens {
			token := Token{
				Network:      t.Network,
				Address:      t.Address,
				TokenBalance: t.TokenBalance,
			}
			if t.TokenAddress != nil {
				token.TokenAddress = *t.TokenAddress
			}
			if t.TokenMetadata != nil {
				token.Decimals = t.TokenMetadata.Decimals
				if t.TokenMetadata.Name != nil {
					token.Name = *t.TokenMetadata.Name
				}
				if t.TokenMetadata.Symbol != nil {
					token.Symbol = *t.TokenMetadata.Symbol
				}
			}
			if t.Error != nil {
				token.Err = *t.Error
			}
			tokens = append(tokens, token)
		}

		pageSeen++
		pageKey = parsed.Data.PageKey
		if pageKey == "" {
			return tokens, netErrs, nil
		}
		if pageSeen >= maxPages {
			return tokens, netErrs, fmt.Errorf(
				"alchemy: more than %d pages of balances for %s; the rest is unread, not absent",
				maxPages, address)
		}
	}
}

// networkErrors flattens the API's network-level failure report.
func networkErrors(parsed tokensResponse) []NetworkError {
	if parsed.Error == nil {
		return nil
	}
	if len(parsed.Error.PartialErrors) == 0 {
		return []NetworkError{{Message: parsed.Error.Message}}
	}
	out := make([]NetworkError, 0, len(parsed.Error.PartialErrors))
	for _, pe := range parsed.Error.PartialErrors {
		out = append(out, NetworkError{Network: pe.Network, Message: pe.Message})
	}
	return out
}

// postTokens issues one Portfolio API request and returns its raw body.
func (c *Client) postTokens(ctx context.Context, payload tokensRequest) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("alchemy: encode request: %w", err)
	}

	// The key is a path segment here, not a header. It therefore must never
	// reach an error message or a log line: the URL is not quoted below for
	// that reason.
	url := fmt.Sprintf("%s/%s/assets/tokens/by-address", c.baseURL, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("alchemy: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alchemy: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alchemy: API status %d for tokens/by-address", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("alchemy: read response: %w", err)
	}
	return body, nil
}
