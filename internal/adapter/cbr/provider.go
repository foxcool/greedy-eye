package cbr

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

const (
	sourceID      = ProviderName
	priceDecimals = uint32(8)
	interval      = "latest"

	// quoteSymbol is the currency every rate is republished in. The feed
	// itself quotes in roubles, which would leave the rouble — the one
	// currency that actually needs converting — with no price row of its own
	// and reachable only by inverting somebody else's. Quoting in USD instead
	// puts both the rouble and a RUB-priced instrument on the direct path
	// through portfolio.crossRate.
	quoteSymbol = "USD"
)

// feedCurrencies are the codes the daily set has carried for years. The list
// selects which catalogue assets a sweep hands this provider; it is not a
// source of truth about the rates, which come from the response alone. A
// currency missing here is simply never asked for — it is not mispriced — and
// one listed but absent from the response comes back as a miss.
// USD is deliberately absent: it is the quote side of every row this provider
// writes, so selecting it would spend a slot on an asset that comes back
// unpriced every single sweep.
var feedCurrencies = []string{
	"AED", "AMD", "AUD", "AZN", "BDT", "BHD", "BOB", "BRL", "BYN", "CAD",
	"CHF", "CNY", "CUP", "CZK", "DKK", "DZD", "EGP", "ETB", "EUR", "GBP",
	"GEL", "HKD", "HUF", "IDR", "INR", "IRR", "JPY", "KGS", "KRW", "KZT",
	"MDL", "MMK", "MNT", "NGN", "NOK", "NZD", "OMR", "PLN", "QAR", "RON",
	"RSD", "SAR", "SEK", "SGD", "THB", "TJS", "TMT", "TRY", "UAH", "UZS",
	"VND", "XDR", "ZAR",

	// The rouble is not in the feed — it is what the feed quotes against —
	// but it is the asset this provider exists for, so it must be selectable.
	"RUB",
}

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client *Client
	log    *slog.Logger
}

// NewProvider wraps a *Client as a Bank of Russia price provider.
func NewProvider(c *Client) *Provider {
	return &Provider{client: c, log: slog.Default()}
}

// BaseAssetSymbol returns the ticker every rate is quoted in.
func (p *Provider) BaseAssetSymbol() string { return quoteSymbol }

// BaseAssetType reports that the quote currency is fiat.
func (p *Provider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

// BudgetExemptSymbols reports the currencies one request covers. The whole
// feed arrives in a single document, so asking for one currency and asking for
// all of them cost the same.
func (p *Provider) BudgetExemptSymbols() []string { return feedCurrencies }

// AssetBudget reports that no asset outside the exempt set is worth asking
// about. Everything this provider can price is in that one document; spending
// a sweep's rotation on the rest of the catalogue would buy nothing but misses.
func (p *Provider) AssetBudget(time.Time, time.Duration) (int, bool) { return 0, true }

// FetchPrices republishes the daily rate set as prices in USD.
//
// Only assets typed forex are considered. A ticker is not evidence of what
// something is: a minted token calling itself EUR must not collect the euro's
// rate, which is the same failure that valued a counterfeit as real USDT
// (personal-c3b, personal-2g8e).
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	candidates := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if currencyAsset(a) {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	rates, err := p.client.DailyRates(ctx)
	if err != nil {
		return nil, err
	}

	// Every rate in the set is stated in roubles per unit, so the USD leg is
	// what turns the set into USD quotes. Without it there is nothing to
	// publish — not even the rouble.
	usdInRUB, ok := rates.RUBPerUnit[quoteSymbol]
	if !ok || !usdInRUB.IsPositive() {
		return nil, fmt.Errorf("cbr: the set for %s carries no %s rate", rates.Date.Format(time.DateOnly), quoteSymbol)
	}

	result := make([]entity.StoredPrice, 0, len(candidates))
	for _, a := range candidates {
		symbol := entity.NormalizeSymbol(a.Symbol)
		value, ok := unitInUSD(rates, usdInRUB, symbol)
		if !ok {
			continue
		}

		last := value.Shift(int32(priceDecimals)).Round(0)
		if !last.IsPositive() {
			// A currency worth less than the smallest storable unit would be
			// stored as zero, and a zero price is not a cheap position — it is
			// a position missing from the total while looking counted.
			p.log.Warn("cbr rate underflows the stored scale, asset left unpriced",
				"symbol", symbol, "asset_id", a.ID, "decimals", priceDecimals)
			continue
		}

		result = append(result, entity.StoredPrice{
			SourceID: sourceID,
			AssetID:  a.ID,
			// BaseAssetID is intentionally empty — resolved by FetchExternalPrices.
			Interval: interval,
			Decimals: priceDecimals,
			Last:     last,
			// The set's own day, not the moment it was fetched. A Friday set
			// read on Sunday is two days old, and saying otherwise would hide
			// exactly the staleness the coverage block exists to report.
			Timestamp: rates.Date,
		})
	}
	return result, nil
}

// unitInUSD converts one unit of the currency into USD. ok is false when the
// set does not carry it.
func unitInUSD(rates *Rates, usdInRUB decimal.Decimal, symbol string) (decimal.Decimal, bool) {
	if symbol == quoteSymbol {
		// The base does not need quoting against itself, and storing 1.0 would
		// give crossRate a self-referential row to divide by.
		return decimal.Zero, false
	}
	if symbol == "RUB" {
		return decimal.NewFromInt(1).Div(usdInRUB), true
	}
	rubPerUnit, ok := rates.RUBPerUnit[symbol]
	if !ok || !rubPerUnit.IsPositive() {
		return decimal.Zero, false
	}
	return rubPerUnit.Div(usdInRUB), true
}

// Asked reports which of these assets the CBR feed is actually asked about.
//
// The daily set is one document covering every currency in it, so the sweep
// hands this provider the whole due list and it reads the forex rows out. The
// rest was never asked: recording them as misses filed the central bank's
// silence against crypto it has no opinion on — 533 such rows on dev, each one
// pushing an asset further into a back-off it did not earn.
func (p *Provider) Asked(assets []*entity.Asset) []*entity.Asset {
	out := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if currencyAsset(a) {
			out = append(out, a)
		}
	}
	return out
}

// currencyAsset reports whether the asset is a currency this feed may speak
// for: typed forex, and not a row standing for one specific on-chain contract.
// The type carries the claim; the market rules out a token that typed itself
// forex to reach a rate it has no right to.
func currencyAsset(a *entity.Asset) bool {
	if a == nil || a.Type != entity.AssetTypeForex {
		return false
	}
	_, onchain := entity.ChainFromOnchainSource(a.Market)
	return !onchain
}
