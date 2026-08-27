package tinvest

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

const (
	sourceID      = ProviderName
	priceDecimals = uint32(8)
	interval      = "latest"

	// batchSize caps how many instrument ids go into one request. The API takes
	// a long array; a request that grows without bound does not.
	batchSize = 100
)

// Markets this provider speaks for, as spelled in assets.market.
const (
	MarketSPBEX = "spbex"
	MarketMOEX  = "moex"
)

// defaultQuoteSymbol is the base declared for the provider as a whole. Every
// row overrides it with the instrument's own currency; this is what a caller
// that ignores the override would get, and dollars are the safer default for a
// venue whose foreign paper is dollar-quoted.
const defaultQuoteSymbol = "USD"

// venues maps our market names onto the settlement venue the API reports.
var venues = map[string][]string{
	MarketSPBEX: {RealExchangeRTS},
	MarketMOEX:  {RealExchangeMOEX},
}

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client  *Client
	catalog *catalog
	log     *slog.Logger
}

// NewProvider wraps a *Client as a T-Invest price provider.
func NewProvider(c *Client) *Provider {
	return &Provider{client: c, catalog: newCatalog(c), log: slog.Default()}
}

// BaseAssetSymbol returns the provider-level quote currency. Individual rows
// carry their own via StoredPrice.BaseSymbol, because one broker response
// prices a foreign share in dollars and a domestic one in roubles.
func (p *Provider) BaseAssetSymbol() string { return defaultQuoteSymbol }

// BaseAssetType reports that the quote currency is fiat.
func (p *Provider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

// AssetBudget reports no per-asset allowance. A broker token has rate limits
// but no metered plan to divide, and one price call covers a hundred
// instruments; a number invented here would look prudent and mean nothing.
func (p *Provider) AssetBudget(time.Time, time.Duration) (int, bool) { return 0, false }

// DiscoverRefs binds assets to the broker's instrument ids.
//
// Identity here is the FIGI, not the ticker: tickers are reassigned and repeat
// across venues, which is the same reason a chain identity is a contract. The
// binding is made only when exactly one instrument on the asset's venue carries
// its ticker — an ambiguous match is reported and left alone rather than
// guessed, because a wrong binding prices a position as somebody else's paper
// and survives every later sweep (personal-c3b on the contract side).
func (p *Provider) DiscoverRefs(ctx context.Context, assets []*entity.Asset) ([]entity.AssetExternalRef, error) {
	var out []entity.AssetExternalRef
	for _, a := range assets {
		if !p.speaksFor(a) || figiOf(a) != "" {
			continue
		}
		exchanges := venues[entity.NormalizeMarket(a.Market)]
		inst, candidates, err := p.catalog.match(ctx, a.Symbol, exchanges)
		if err != nil {
			// The catalogue could not be consulted at all. That is not the same
			// claim as "this ticker is unknown", so nothing is bound and the
			// error travels up rather than turning into a silent miss.
			return out, err
		}
		if candidates != 1 {
			p.log.Debug("tinvest ticker does not resolve to exactly one instrument, no binding made",
				"symbol", a.Symbol, "market", a.Market, "candidates", candidates)
			continue
		}
		out = append(out, entity.AssetExternalRef{
			AssetID: a.ID,
			Source:  RefSource,
			Ref:     inst.FIGI,
			Origin:  entity.RefOriginAuto,
		})
	}
	return out, nil
}

// FetchPrices prices broker-listed instruments in the currency each is quoted
// in.
//
// Selection is by market, like every other price adapter: assets.market is the
// listing venue, and the shape of a ticker is not evidence of anything.
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	byFIGI := make(map[string]*entity.Asset, len(assets))
	figis := make([]string, 0, len(assets))
	for _, a := range assets {
		if !p.speaksFor(a) {
			continue
		}
		figi := figiOf(a)
		if figi == "" {
			// Unbound: DiscoverRefs either found nothing or found too much, and
			// both were reported there. Pricing by ticker here would reintroduce
			// exactly the guess that binding exists to avoid.
			continue
		}
		if _, seen := byFIGI[figi]; seen {
			continue
		}
		byFIGI[figi] = a
		figis = append(figis, figi)
	}
	if len(figis) == 0 {
		return nil, nil
	}

	var result []entity.StoredPrice
	var lastErr error
	for start := 0; start < len(figis); start += batchSize {
		end := min(start+batchSize, len(figis))
		batch := figis[start:end]

		prices, err := p.client.LastPrices(ctx, batch)
		if err != nil {
			// Partial by design: one failing batch must not cost the portfolio
			// the prices another batch already answered with.
			lastErr = err
			p.log.Warn("tinvest last prices batch failed", "error", err)
			continue
		}

		trading := p.tradingByFIGI(ctx, batch)

		for _, lp := range prices {
			asset, ok := byFIGI[lp.FIGI]
			if !ok {
				continue
			}
			inst, known, err := p.catalog.instrument(ctx, lp.FIGI)
			if err != nil {
				lastErr = err
				continue
			}
			price, ok := p.storedPrice(asset, lp, inst, known, trading[lp.FIGI])
			if !ok {
				continue
			}
			result = append(result, price)
		}
	}

	if len(result) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return result, nil
}

// tradingByFIGI reads which instruments are trading right now. Best-effort: a
// failed status call must not stop pricing, and an absent status is handled the
// same way as "not trading" — the conservative direction, which keeps the price
// out of the total rather than into it.
func (p *Provider) tradingByFIGI(ctx context.Context, figis []string) map[string]TradingStatus {
	statuses, err := p.client.TradingStatuses(ctx, figis)
	if err != nil {
		p.log.Warn("tinvest trading statuses failed, prices treated as untraded", "error", err)
		return nil
	}
	out := make(map[string]TradingStatus, len(statuses))
	for _, s := range statuses {
		out[s.FIGI] = s
	}
	return out
}

// storedPrice turns one last price into a price row, or reports that it cannot
// honestly become one.
//
// The whole difficulty of this adapter is here. The API reports no turnover at
// all, and marketdepth.Thin treats an absent volume as "no claim" rather than a
// thin market — deliberately, because receipt tokens have real value and no
// market of their own. That default is right for crypto and wrong here: a
// sanctioned share whose last trade was in 2022 would arrive with a price, no
// volume, and sail straight into the total at a figure nobody can transact at.
//
// So the market claim is made explicitly:
//
//   - Trading normally, price printed on the exchange → a trade made this
//     number and there is a market behind it. No volume is claimed, because
//     none was measured, and the freshness axis dates the row.
//
//   - Anything else — halted, delisted, blocked, or a dealer's quote → turnover
//     of zero. That is a measurement and not an assumption: an instrument that
//     cannot be traded traded nothing. ADR-009 then keeps the number out of the
//     total while it stays in the catalogue, which is exactly what PR#75 did
//     with an exchange's recognised close.
func (p *Provider) storedPrice(
	asset *entity.Asset, lp LastPrice, inst Instrument, known bool, status TradingStatus,
) (entity.StoredPrice, bool) {
	value, ok := lp.Price.Decimal()
	if !ok || !value.IsPositive() {
		return entity.StoredPrice{}, false
	}

	// Without the instrument there is no currency, and a price whose currency is
	// a guess is worse than no price: publishing a dollar figure as roubles is a
	// hundredfold error, not a rounding one.
	if !known || inst.Currency == "" {
		p.log.Warn("tinvest instrument is not in the catalogue, asset left unpriced",
			"figi", lp.FIGI, "symbol", asset.Symbol)
		return entity.StoredPrice{}, false
	}

	at := lp.Time
	if at.IsZero() {
		// The exchange did not date its own print. Dating it "now" would claim a
		// freshness nobody measured, so the row is dropped instead.
		return entity.StoredPrice{}, false
	}

	price := entity.StoredPrice{
		SourceID: sourceID,
		AssetID:  asset.ID,
		// BaseAssetID is resolved by FetchExternalPrices from BaseSymbol.
		BaseSymbol: strings.ToUpper(inst.Currency),
		Interval:   interval,
		Decimals:   priceDecimals,
		Last:       value.Shift(int32(priceDecimals)).Round(0),
		Timestamp:  at,
	}

	if tradedNow(lp, inst, status) {
		price.Provenance = entity.PriceProvenanceTraded
	} else {
		price.Provenance = provenanceOf(lp)
		price.Volume = decimal.NullDecimal{Decimal: decimal.Zero, Valid: true}
	}

	if !price.Last.IsPositive() {
		return entity.StoredPrice{}, false
	}
	return price, true
}

// tradedNow reports whether there is a market behind this print at this moment.
//
// Every condition has to hold: an instrument can be in normal trading and still
// be quoted by a dealer, and it can be quotable through the catalogue while the
// broker has it blocked. An absent status counts as not trading, because the
// question was asked and not answered.
func tradedNow(lp LastPrice, inst Instrument, status TradingStatus) bool {
	if status.TradingStatus != StatusNormalTrading {
		return false
	}
	if inst.BlockedTCAFlag || !inst.APITradeAvailableFlag {
		return false
	}
	return lp.LastPriceType == LastPriceExchange
}

// provenanceOf says what produced a number that is not a live market print.
//
// A dealer quote is an appraisal: a market maker stated it, no trade made it.
// A halted instrument's last price is still a trade, just an old one, and
// calling that an appraisal would misdescribe it — its problem is age, which
// the freshness axis reports, and absence of a market today, which the zero
// turnover beside it reports.
func provenanceOf(lp LastPrice) entity.PriceProvenance {
	if lp.LastPriceType == LastPriceExchange {
		return entity.PriceProvenanceTraded
	}
	return entity.PriceProvenanceAppraised
}

// Asked reports which of these assets T-Invest is actually asked about.
//
// Both of FetchPrices' filters apply, and the second matters as much as the
// first: an asset with no FIGI binding is never priced here, so recording it as
// a miss would blame the venue for a binding we have not made. The remedy for
// an unbound asset is DiscoverRefs, not a back-off.
func (p *Provider) Asked(assets []*entity.Asset) []*entity.Asset {
	out := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if p.speaksFor(a) && figiOf(a) != "" {
			out = append(out, a)
		}
	}
	return out
}

// speaksFor reports whether this provider prices the asset at all.
func (p *Provider) speaksFor(a *entity.Asset) bool {
	if a == nil || a.Symbol == "" {
		return false
	}
	if _, ok := venues[entity.NormalizeMarket(a.Market)]; !ok {
		return false
	}
	switch a.Type {
	case entity.AssetTypeStock, entity.AssetTypeFund:
		return true
	default:
		// Bonds are quoted as a percentage of nominal and need the same face
		// value handling MOEX has. Adding them on the theory that they might
		// work would publish percentages as money.
		return false
	}
}

// figiOf reads the asset's broker instrument id from its external refs. Refs are
// loaded only on the pricing path, so an asset without them yields "" and is
// left to DiscoverRefs rather than guessed at.
func figiOf(a *entity.Asset) string {
	for _, ref := range a.ExternalRefs {
		if strings.EqualFold(ref.Source, RefSource) && ref.Ref != "" {
			return ref.Ref
		}
	}
	return ""
}
