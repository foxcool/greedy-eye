package binance

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

const (
	// ProviderName is the canonical source identifier for Binance prices.
	ProviderName = "binance"

	sourceID      = ProviderName
	priceDecimals = uint32(8)
	interval      = "latest"
)

// symbolUniverseTTL bounds how long one exchangeInfo snapshot is trusted. The
// listing set changes on the order of days, the sweep runs hourly, and the
// download is several megabytes — so refreshing it per sweep would cost far
// more than it could ever save. A pair listed since the last refresh is simply
// not asked about until the next one, which is the safe direction: the cost of
// waiting is one stale hour, the cost of guessing is the whole batch.
const symbolUniverseTTL = 12 * time.Hour

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client *Client

	mu       sync.Mutex
	loadedAt time.Time
	// tradable holds the venue symbols currently in TRADING status. Nil means
	// never loaded; empty-but-loaded cannot happen, because a successful
	// exchangeInfo always lists thousands of pairs.
	tradable map[string]bool
}

// NewProvider wraps a *Client as a Binance price provider.
func NewProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// tradableSymbols returns the cached listing universe, refreshing it when the
// snapshot has expired.
//
// A failure to load is NOT reported as an empty universe. The distinction is
// the same one marketdepth.Thin and the CoinGecko contract catalog already
// make: absence of evidence is not evidence of absence, and treating an
// unreachable exchangeInfo as "nothing is listed" would silently stop pricing
// everything. On failure the caller falls back to asking as before.
func (p *Provider) tradableSymbols(ctx context.Context) (map[string]bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.tradable != nil && time.Since(p.loadedAt) <= symbolUniverseTTL {
		return p.tradable, true
	}

	symbols, err := p.client.ListTradableSymbols(ctx)
	if err != nil || len(symbols) == 0 {
		// Keep serving a stale snapshot over serving none: a listing set from
		// twelve hours ago is a far better filter than no filter at all.
		if p.tradable != nil {
			return p.tradable, true
		}
		return nil, false
	}

	index := make(map[string]bool, len(symbols))
	for _, sym := range symbols {
		index[sym] = true
	}
	p.tradable = index
	p.loadedAt = time.Now()
	return index, true
}

// BaseAssetSymbol returns the ticker of the quote currency used by Binance ("USDT").
func (p *Provider) BaseAssetSymbol() string { return "USDT" }

// BaseAssetType reports that Binance's quote currency (USDT) is a cryptocurrency stablecoin.
func (p *Provider) BaseAssetType() entity.AssetType { return entity.AssetTypeCryptocurrency }

// speaksFor reports whether this provider prices the asset at all.
//
// Selection is by market, like every other price adapter: assets.market is the
// listing venue. It matters more here than anywhere else, because this provider
// still identifies an instrument by its TICKER (see FetchPrices), and a ticker
// is minted by whoever pays the gas. An asset isolated on its own contract
// market — entity.ContractMarket, the quarantine a counterfeit lands in — must
// never be asked about under a name it merely claims, or the counterfeit is
// priced as the real thing and the whole point of the isolation is undone.
//
// This is a floor, not the fix. A counterfeit that reached the global crypto
// market without a verdict is still exposed; binding to the listing is what
// closes that (personal-avm.1).
func (p *Provider) speaksFor(a *entity.Asset) bool {
	return a != nil && entity.NormalizeMarket(a.Market) == entity.MarketCrypto
}

// FetchPrices fetches current prices from Binance for the given assets.
// Binance symbols are derived from asset symbols as UPPER(symbol)+"USDT"
// (e.g., asset.Symbol="BTC" → "BTCUSDT").
// Assets whose symbols produce no Binance response are silently skipped.
// Asked reports which of these assets Binance will actually be asked about, so
// the sweep credits an attempt only where a request is really made.
//
// Two filters, and the second is the point of personal-edtu: the asset must be
// one this provider speaks for, and its derived pair must be listed. An
// airdropped jetton is neither priced nor recorded as a miss — before this, it
// was recorded, and the back-off it accrued was indistinguishable from that of
// a coin Binance had genuinely stopped quoting.
func (p *Provider) Asked(assets []*entity.Asset) []*entity.Asset {
	// Uses the cached universe only: Asked is called on the sweep's hot path
	// and must not trigger a multi-megabyte download of its own. FetchPrices
	// refreshes the snapshot, so the two agree within one sweep.
	p.mu.Lock()
	tradable := p.tradable
	fresh := tradable != nil && time.Since(p.loadedAt) <= symbolUniverseTTL
	p.mu.Unlock()

	out := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if !p.speaksFor(a) {
			continue
		}
		// Without a usable snapshot every speaks-for asset is asked, which is
		// the old behaviour: an unknown universe must not silence the provider.
		if fresh && !tradable[venueSymbol(a)] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// venueSymbol derives the Binance pair for an asset.
//
// Identity here is still the ticker and nothing else, which is why the pair has
// to be checked against the listing set before it is asked for — and why a
// counterfeit that reaches the global crypto market is still exposed until the
// binding lands (personal-avm.1 / personal-psu.2). This function is where that
// derivation is named, so there is one place to replace when it does.
func venueSymbol(a *entity.Asset) string {
	return strings.ToUpper(a.Symbol) + "USDT"
}

func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	if len(assets) == 0 {
		return nil, nil
	}

	// Candidates first, universe second: an asset set this provider speaks for
	// nothing in must cost no request at all, not even the listing download.
	candidates := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if p.speaksFor(a) {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// Refreshed here rather than in Asked: this is the call that can afford a
	// download, and it runs once per sweep per provider.
	tradable, haveUniverse := p.tradableSymbols(ctx)

	// Build Binance symbol → assetID map and collect symbols to fetch.
	symbolToAsset := make(map[string]string, len(candidates))
	symbols := make([]string, 0, len(candidates))
	for _, a := range candidates {
		sym := venueSymbol(a)
		// An unlisted pair costs its own quote and nothing else: excluded up
		// front, it can no longer take a batch of good symbols down with it.
		if haveUniverse && !tradable[sym] {
			continue
		}
		symbolToAsset[sym] = a.ID
		symbols = append(symbols, sym)
	}
	if len(symbols) == 0 {
		return nil, nil
	}

	tickers, err := p.client.GetTickerPrices(ctx, symbols)
	if err != nil {
		return nil, fmt.Errorf("binance: %w", err)
	}

	now := time.Now()
	result := make([]entity.StoredPrice, 0, len(tickers))
	for _, t := range tickers {
		assetID, ok := symbolToAsset[t.Symbol]
		if !ok {
			continue
		}
		price, err := decimal.NewFromString(t.Price)
		if err != nil {
			continue
		}
		// Store as a raw integer scaled by priceDecimals (value = last / 10^priceDecimals).
		last := price.Shift(int32(priceDecimals)).Round(0)
		result = append(result, entity.StoredPrice{
			SourceID: sourceID,
			AssetID:  assetID,
			// BaseAssetID is intentionally empty — resolved by FetchExternalPrices handler.
			Interval:  interval,
			Decimals:  priceDecimals,
			Last:      last,
			Timestamp: now,
		})
	}
	return result, nil
}
