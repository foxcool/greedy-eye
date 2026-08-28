package binance

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

const (
	// ProviderName is the canonical source identifier for Binance prices.
	ProviderName = "binance"

	// RefSource names the binding this provider owns in asset_external_refs.
	// The ref itself is the venue pair ("BTCUSDT"): Binance has no contract
	// namespace, so the listing is the only identity it publishes.
	RefSource = ProviderName

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
	log    *slog.Logger

	mu       sync.Mutex
	loadedAt time.Time
	// tradable holds the venue symbols currently in TRADING status. Nil means
	// never loaded; empty-but-loaded cannot happen, because a successful
	// exchangeInfo always lists thousands of pairs.
	tradable map[string]bool
}

// NewProvider wraps a *Client as a Binance price provider.
func NewProvider(client *Client) *Provider {
	return &Provider{client: client, log: slog.Default()}
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
// This is the floor. The fix above it is the binding: an asset on the global
// crypto market is priced only once DiscoverRefs has tied it to a listed pair
// that nothing else claims (personal-psu.2 / personal-avm.1).
// reportedVolume scales Binance's 24h quote turnover the way the price beside it
// is scaled, because marketdepth.Thin divides both by the same Decimals.
//
// An unparseable or non-positive figure is left unreported rather than stored as
// zero. The distinction is load-bearing: a reported zero counts as thin and drops
// the holding out of the total, while "not reported" leaves it in — and a parse
// failure is our ignorance, not evidence about the market. Same rule as
// coingecko.reported.
func reportedVolume(raw string) decimal.NullDecimal {
	if raw == "" {
		return decimal.NullDecimal{}
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || !v.IsPositive() {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: v.Shift(int32(priceDecimals)).Round(0), Valid: true}
}

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
		// Unbound assets are not asked about, so they accrue no miss either.
		// That is the point of personal-edtu applied to this gate: a token that
		// was never eligible for a Binance quote must not collect back-off as
		// though Binance had refused it.
		pair := pairOf(a)
		if pair == "" {
			continue
		}
		// Without a usable snapshot every bound asset is asked, which is the old
		// behaviour: an unknown universe must not silence the provider.
		if fresh && !tradable[pair] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// venueSymbol derives the Binance pair an asset would claim.
//
// This is a CLAIM, not an identity: the ticker is minted by whoever pays the
// gas, so the derivation only proposes a pair for DiscoverRefs to confirm. The
// confirmed answer lives in asset_external_refs and is read by pairOf.
func venueSymbol(a *entity.Asset) string {
	return strings.ToUpper(a.Symbol) + "USDT"
}

// pairOf returns the venue pair this asset is bound to, empty when unbound.
func pairOf(a *entity.Asset) string {
	for _, ref := range a.ExternalRefs {
		if strings.EqualFold(ref.Source, RefSource) && ref.Ref != "" {
			return ref.Ref
		}
	}
	return ""
}

// DiscoverRefs binds assets to the Binance pairs they legitimately claim.
//
// The rule is personal-avm.1's, the one MOEX and T-Invest already follow: a
// price may only land on an asset bound to a listed instrument, and the binding
// is made only when the match is unambiguous. What differs here is WHERE the
// ambiguity lives. A curated venue is never ambiguous on its own side — Binance
// has exactly one BTCUSDT — so the question is not "which pair does this ticker
// mean" but "which of OUR assets is entitled to it". Two assets claiming USDT
// is the whole problem: a token minted with a famous symbol would otherwise be
// handed the real coin's price (personal-psu.2), which is способ #1 reopened in
// a second source.
//
// So candidacy is counted across the batch, and a contested pair binds nobody.
// Both claimants stay unpriced and are disclosed by ValuationCoverage, which is
// the honest outcome: the adapter cannot tell the counterfeit from the original,
// and inventing a tie-break here would be a guess in the direction where
// guessing is unsafe. A human resolves it by quarantining the impostor, and the
// next sweep binds whoever is left alone.
//
// A pair absent from the listing set binds nothing either, and the unreachable
// case is kept distinct from the unlisted one: with no usable snapshot nothing
// is bound at all, because "we could not ask" is not the claim "it is not
// listed" (the same distinction tradableSymbols makes).
func (p *Provider) DiscoverRefs(ctx context.Context, assets []*entity.Asset) ([]entity.AssetExternalRef, error) {
	tradable, haveUniverse := p.tradableSymbols(ctx)
	if !haveUniverse {
		return nil, fmt.Errorf("binance: listing set unavailable, nothing bound")
	}

	// Count claimants per pair before binding any of them: a decision made
	// while walking the list would bind whichever asset came first.
	claims := make(map[string]int, len(assets))
	for _, a := range assets {
		if !p.speaksFor(a) || pairOf(a) != "" {
			continue
		}
		claims[venueSymbol(a)]++
	}

	var out []entity.AssetExternalRef
	for _, a := range assets {
		if !p.speaksFor(a) || pairOf(a) != "" {
			continue
		}
		sym := venueSymbol(a)
		if !tradable[sym] {
			continue
		}
		if claims[sym] != 1 {
			if p.log != nil {
				p.log.Debug("binance pair is claimed by more than one asset, no binding made",
					"symbol", a.Symbol, "pair", sym, "claimants", claims[sym])
			}
			continue
		}
		out = append(out, entity.AssetExternalRef{
			AssetID: a.ID,
			Source:  RefSource,
			Ref:     sym,
			Origin:  entity.RefOriginAuto,
		})
	}
	return out, nil
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
		// Bound assets only. Deriving the pair here instead would reintroduce
		// the exact guess the binding exists to avoid: an impostor carrying a
		// famous ticker would be handed the real coin's price, and the quote
		// would then survive every later sweep as though it had been earned.
		// Unbound means DiscoverRefs found nothing or found a contest, and both
		// were reported there.
		sym := pairOf(a)
		if sym == "" {
			continue
		}
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
			Volume:    reportedVolume(t.QuoteVolume),
			Timestamp: now,
		})
	}
	return result, nil
}
