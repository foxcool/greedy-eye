package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// chainPlatform maps an internal chain identifier (the one carried on
// entity.WalletBalance and encoded into an "onchain:<chain>" ref source) to the
// CoinGecko asset platform ID. Only chains whose balances can carry a contract
// address are listed: a native coin has no contract to confirm.
var chainPlatform = map[string]string{
	"eth":       "ethereum",
	"base":      "base",
	"arbitrum":  "arbitrum-one",
	"optimism":  "optimistic-ethereum",
	"linea":     "linea",
	"polygon":   "polygon-pos",
	"bsc":       "binance-smart-chain",
	"avalanche": "avalanche",
	"solana":    "solana",
	"ton":       "the-open-network",
}

// contractCatalogTTL bounds how long one /coins/list snapshot is trusted. The
// catalog is a full-universe download (a few MB), so it is fetched at most once
// per TTL per client; newly listed tokens are simply unconfirmed until the next
// refresh, which is the safe direction.
const contractCatalogTTL = 24 * time.Hour

// coinListItem is the JSON shape from /coins/list?include_platform=true.
type coinListItem struct {
	ID        string            `json:"id"`
	Symbol    string            `json:"symbol"`
	Platforms map[string]string `json:"platforms"`
}

// ListCoinsWithPlatforms downloads the full coin list including the contract
// address of every coin on every platform it is deployed to. One request covers
// the entire universe, which is what makes contract confirmation affordable
// during a sync — the per-contract endpoint would cost one request per token.
func (c *Client) ListCoinsWithPlatforms(ctx context.Context) ([]coinListItem, error) {
	url := c.baseURL + "/coins/list?include_platform=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.authenticate(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from CoinGecko", resp.StatusCode)
	}

	var items []coinListItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return items, nil
}

// contractIndex answers "which listed coin does this contract belong to?" from a
// cached snapshot of the CoinGecko catalog. It is the authority the asset
// resolver consults before letting an unknown contract merge into an existing
// ticker (personal-c3b): a counterfeit mints a contract nobody has listed, so a
// miss is the signal that the token is not what its ticker claims.
type contractIndex struct {
	client *Client
	ttl    time.Duration

	mu       sync.Mutex
	loadedAt time.Time
	// coinByContract is keyed by "<platform>/<lowercased address>" and holds the
	// coin the contract belongs to: its id and the ticker it is listed under.
	// The id is kept because the ticker is not an identity — this catalogue puts
	// several coins under one ticker on one chain (personal-dvgm) — and it costs
	// nothing to keep, arriving in the same response.
	coinByContract map[string]coinRef
}

// coinRef is one catalogue entry, private to the adapter that downloads it: it
// describes a listing, not an asset, and it crosses no package boundary.
type coinRef struct {
	id     string
	symbol string
}

func newContractIndex(c *Client) *contractIndex {
	return &contractIndex{client: c, ttl: contractCatalogTTL}
}

// lookup reports the coin a contract on a chain belongs to. found is false when
// the chain has no CoinGecko platform or the contract is not listed; an error
// means the catalog could not be consulted at all, which the caller must not
// read as "unlisted".
func (x *contractIndex) lookup(ctx context.Context, chain, address string) (coin coinRef, found bool, err error) {
	platform, ok := chainPlatform[strings.ToLower(strings.TrimSpace(chain))]
	if !ok {
		return coinRef{}, false, nil
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return coinRef{}, false, nil
	}

	x.mu.Lock()
	defer x.mu.Unlock()
	// The lock is held across the fetch on purpose: concurrent syncs would
	// otherwise each download the full catalog to build the same map.
	if x.coinByContract == nil || time.Since(x.loadedAt) > x.ttl {
		items, ferr := x.client.ListCoinsWithPlatforms(ctx)
		if ferr != nil {
			return coinRef{}, false, fmt.Errorf("coingecko contract catalog: %w", ferr)
		}
		index := make(map[string]coinRef, len(items)*2)
		for _, it := range items {
			for plat, addr := range it.Platforms {
				addr = strings.ToLower(strings.TrimSpace(addr))
				if plat == "" || addr == "" {
					continue
				}
				index[plat+"/"+addr] = coinRef{
					id:     it.ID,
					symbol: strings.ToUpper(it.Symbol),
				}
			}
		}
		x.coinByContract = index
		x.loadedAt = time.Now()
	}

	coin, found = x.coinByContract[platform+"/"+address]
	return coin, found, nil
}

// ResolveContract confirms a contract's identity: it returns the coin CoinGecko
// lists that contract under — its coin id and the ticker it publishes. A false
// ok means the contract is not listed (or the chain is not covered) and the
// caller must treat the token as an unverified instrument rather than an
// instance of a known ticker.
func (p *Provider) ResolveContract(ctx context.Context, chain, address string) (coinID, symbol string, listed bool, err error) {
	coin, found, err := p.contracts.lookup(ctx, chain, address)
	return coin.id, coin.symbol, found, err
}
