package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// rankedMarketItem is the part of a /coins/markets row a ranking reads. The
// window changes are pointers because the source answers null for a window it
// has no history for, and zero would draw that as "flat".
type rankedMarketItem struct {
	ID            string   `json:"id"`
	Symbol        string   `json:"symbol"`
	Name          string   `json:"name"`
	CurrentPrice  float64  `json:"current_price"`
	MarketCap     float64  `json:"market_cap"`
	MarketCapRank *int     `json:"market_cap_rank"`
	TotalVolume   float64  `json:"total_volume"`
	Change24h     *float64 `json:"price_change_percentage_24h_in_currency"`
	Change7d      *float64 `json:"price_change_percentage_7d_in_currency"`
	Change30d     *float64 `json:"price_change_percentage_30d_in_currency"`
}

// TopByMarketCap reads the n largest coins by capitalisation, in USD, with
// rank, capitalisation, 24h volume and the 24h/7d/30d changes.
//
// One request whatever n and whichever window the caller draws: the endpoint
// returns all three windows on one page of up to marketsPageSize coins, and a
// page costs one call of the plan however many rows it carries. A larger n
// would need a second page and is refused rather than silently doubling the
// cost.
func (p *Provider) TopByMarketCap(ctx context.Context, n int) (entity.MarketRanking, error) {
	if n < 1 || n > marketsPageSize {
		return entity.MarketRanking{}, fmt.Errorf("ranking size %d outside 1..%d", n, marketsPageSize)
	}
	c := p.client
	query := url.Values{
		"vs_currency":             {cgQuoteCurrency},
		"order":                   {"market_cap_desc"},
		"per_page":                {strconv.Itoa(n)},
		"page":                    {"1"},
		"sparkline":               {"false"},
		"price_change_percentage": {"24h,7d,30d"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/coins/markets?"+query.Encode(), nil)
	if err != nil {
		return entity.MarketRanking{}, fmt.Errorf("create request: %w", err)
	}
	c.authenticate(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return entity.MarketRanking{}, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return entity.MarketRanking{}, c.statusError(resp.StatusCode)
	}

	var items []rankedMarketItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return entity.MarketRanking{}, fmt.Errorf("decode response: %w", err)
	}

	out := entity.MarketRanking{
		Source:   ProviderName,
		Currency: cgQuoteCurrency,
		Coins:    make([]entity.RankedCoin, 0, len(items)),
		ReadAt:   time.Now(),
	}
	for _, it := range items {
		// A coin without a rank has no capitalisation the source vouches for;
		// it trails the page and is not part of a ranking.
		if it.MarketCapRank == nil || *it.MarketCapRank < 1 {
			continue
		}
		out.Coins = append(out.Coins, entity.RankedCoin{
			CoinID:    it.ID,
			Symbol:    strings.ToUpper(it.Symbol),
			Name:      it.Name,
			Rank:      *it.MarketCapRank,
			Price:     it.CurrentPrice,
			MarketCap: it.MarketCap,
			Volume24h: it.TotalVolume,
			Change24h: it.Change24h,
			Change7d:  it.Change7d,
			Change30d: it.Change30d,
		})
	}
	return out, nil
}

// RankedCoinID reports the coin a catalogue asset is priced as. Only the
// curated path names a coin id: a contract-priced token is asked about by
// address, and the ranking does not carry addresses.
func (p *Provider) RankedCoinID(a *entity.Asset) (string, bool) {
	return curatedCoinID(a)
}

// curatedCoinID is the one rule for which assets the curated symbol map
// prices: the global crypto market only. A per-contract row merely carrying a
// famous ticker is an unverified instrument, and pricing it from the map is how
// a counterfeit USDT was valued as the real one.
func curatedCoinID(a *entity.Asset) (string, bool) {
	if a.Market != entity.MarketCrypto {
		return "", false
	}
	id, ok := nativeCoinID[strings.ToLower(a.Symbol)]
	return id, ok
}
