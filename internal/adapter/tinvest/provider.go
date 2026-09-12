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

// marketOfVenue inverts venues so a position can be told which of our markets
// its instrument lists on. Derived from the same table rather than written out
// again: two hand-kept copies of this mapping would disagree eventually, and
// the disagreement would show up as one company owning two asset rows.
var marketOfVenue = func() map[string]string {
	out := make(map[string]string, len(venues))
	for market, exchanges := range venues {
		for _, exchange := range exchanges {
			out[exchange] = market
		}
	}
	return out
}()

// MarketOf names the market an instrument lists on, or "" when the settlement
// venue is one this system has no market for. Empty is a real answer: an asset
// row needs a market it can be identified by, and inventing one binds the
// position to a catalogue entry nothing else will ever match.
func MarketOf(inst Instrument) string {
	// Normalised, because onVenue right next door compares with EqualFold and
	// TrimSpace: two neighbouring readings of the same field that disagree
	// about whitespace is how one of them starts silently returning nothing.
	return marketOfVenue[strings.ToUpper(strings.TrimSpace(inst.RealExchange))]
}

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client  *Client
	catalog *catalog
	log     *slog.Logger
	// now is the clock the market claim is measured against. Injectable because
	// noMarketBehind compares a print's age to a threshold, and a test that
	// asked the wall clock would start failing two days after it was written.
	now func() time.Time
}

// NewProvider wraps a *Client as a T-Invest price provider.
func NewProvider(c *Client) *Provider {
	return &Provider{client: c, catalog: newCatalog(c), log: slog.Default(), now: time.Now}
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

	// One reading of the clock for the whole sweep. noMarketBehind compares a
	// print's age against it, and asking per row would let the horizon fall
	// between two instruments of the same batch.
	now := p.now()

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
			price, ok := p.storedPrice(asset, lp, inst, known, trading[lp.FIGI], now)
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

// tradingByFIGI reads what the venue says about each instrument right now. Two
// fields are read from the answer — the trading status and whether the API may
// trade the instrument at all — and both are live counterparts of copies the
// catalogue already carries. A missing answer therefore costs nothing: the
// catalogue still speaks, and refusesToTrade lets either source refuse without
// letting either one grant permission.
//
// Best-effort: a failed status call must not stop pricing.
func (p *Provider) tradingByFIGI(ctx context.Context, figis []string) map[string]TradingStatus {
	statuses, err := p.client.TradingStatuses(ctx, figis)
	if err != nil {
		p.log.Warn("tinvest trading statuses failed, the catalogue snapshot answers alone", "error", err)
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
//   - The price printed on the exchange, on paper the venue will let anyone
//     trade → a trade made this number and there is a market behind it. No
//     volume is claimed, because none was measured, and the freshness axis
//     dates the row.
//
//   - Paper that cannot be traded at all, or a dealer's quote → turnover of
//     zero. That is a measurement and not an assumption: an instrument that
//     cannot be traded traded nothing. ADR-009 then keeps the number out of the
//     total while it stays in the catalogue, which is exactly what PR#75 did
//     with an exchange's recognised close.
//
// A CLOSED SESSION IS NEITHER. See noMarketBehind.
func (p *Provider) storedPrice(
	asset *entity.Asset, lp LastPrice, inst Instrument, known bool, status TradingStatus, now time.Time,
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

	price.Provenance = provenanceOf(lp)
	if noMarketBehind(price.Provenance, inst, status, at, now) {
		price.Volume = decimal.NullDecimal{Decimal: decimal.Zero, Valid: true}
	}

	if !price.Last.IsPositive() {
		return entity.StoredPrice{}, false
	}
	return price, true
}

// noMarketBehind reports whether this print stands on no market at all, as
// opposed to standing on one that is merely shut at the moment of asking.
//
// BOTH HALVES ARE LOAD-BEARING, AND NEITHER IS ENOUGH ALONE. That is not a
// design preference, it is what the venue's own answers permit. Asked about
// three instruments at one instant, with the main session shut, the live API
// said:
//
//	a share trading in the evening session   blockedTca=false apiTrade=true  DEALER_NORMAL_TRADING
//	a share whose session had not opened     blockedTca=false apiTrade=true  NOT_AVAILABLE_FOR_TRADING
//	a fund frozen by sanctions since 2022    blockedTca=false apiTrade=true  NOT_AVAILABLE_FOR_TRADING
//
// Paper frozen for years carries exactly the flags of a healthy share, so
// neither flag can name an instrument that cannot be traded. And one status is
// worn both by the fund that will never trade again and by the share that has
// simply not opened yet — one code for two different claims. The only fact that
// separates them is how long ago somebody last traded.
//
// So the rule is the conjunction: the venue refuses to trade this instrument AND
// nobody has traded it within noTradeHorizon.
//
// THE FLAGS SIT INSIDE THE CONJUNCTION TOO, and that is a change of its own.
// They used to zero a row by themselves, on the reading that BlockedTCAFlag
// names paper nobody may trade; the measurement above says otherwise, and a
// flag that misses the instrument it was chosen for does not get to remove a
// position unaided. Paper blocked today therefore stays in the total for a
// month, priced by the trade that really happened, with its age on show.
//
// THAT IS A CLAIM ABOUT DEPTH CARRYING A CLAIM ABOUT LIQUIDITY, and it is the
// known cost of this whole function. Frozen paper has a real price and no way
// out of the position, which belongs on a liquidity axis beside holdings already
// marked `liquidity: locked` — not on ADR-009's gate (personal-dkae). Until that
// axis exists, this gate is the only thing standing between a years-old print
// and the total.
//
// The rule this replaced asked for NORMAL_TRADING at the moment of asking, and
// so stamped a turnover of zero on every instrument the minute its session
// closed: an equity portfolio dropped by a fifth every evening with nothing
// bought or sold, and recovered each morning by itself (personal-5be7). The
// first row above shows how narrow that test was — an evening dealer session is
// trading, and it is not the one constant the test allowed.
func noMarketBehind(
	p entity.PriceProvenance, inst Instrument, status TradingStatus, printedAt, now time.Time,
) bool {
	// A dealer's quote is an appraisal: a market maker stated it, no trade made
	// it. Telling those apart is what PriceProvenance exists for.
	if p != entity.PriceProvenanceTraded {
		return true
	}
	return refusesToTrade(inst, status) && now.Sub(printedAt) > noTradeHorizon
}

// refusesToTrade reports the venue's current answer to "may this be traded".
//
// IT IS A WHITELIST, AND THE CONJUNCTION IN noMarketBehind IS WHAT MAKES THAT
// SAFE. A blacklist would have to enumerate every way this API can spell a
// refusal, and the enum spells it at least twice — NOT_AVAILABLE_FOR_TRADING and
// DEALER_NOT_AVAILABLE_FOR_TRADING — while also carrying UNSPECIFIED, which
// proto3 JSON omits from the payload altogether, so an absent field and a
// refusal arrive looking the same. Matching the one constant that was measured
// would have let a 2022 print into the total on any hour when the venue answers
// in its dealer dialect. Every unrecognised spelling therefore counts as a
// refusal, and a status this adapter has never seen fails towards keeping paper
// out rather than letting it in.
//
// That direction is only affordable because a refusal on its own does nothing:
// the print also has to be older than noTradeHorizon. A live instrument whose
// status is briefly unreadable is a month away from being affected, and by then
// it has printed again.
//
// The live answer wins when there is one, and the catalogue's copy is the
// fallback. Both are read with the same eyes: the catalogue's field is a snapshot
// up to catalogTTL old, which is why it may not decide anything by itself.
func refusesToTrade(inst Instrument, status TradingStatus) bool {
	// The catalogue's flags say the broker will not let this be traded at all.
	// They used to zero a row on their own; they no longer do, because the live
	// API shows them clean on paper frozen since 2022 — a flag that misses the
	// case it names cannot be trusted to fire alone.
	if inst.BlockedTCAFlag || !inst.APITradeAvailableFlag {
		return true
	}
	// The same statement from the live record, which is fresher than a
	// catalogue snapshot up to catalogTTL old.
	if status.FIGI != "" && !status.APITradeAvailableFlag {
		return true
	}
	// EITHER SOURCE MAY REFUSE, AND NEITHER MAY GRANT PERMISSION OVER THE OTHER.
	// The live answer used to replace the catalogue's, which turned out to be a
	// way back into the total: the whitelist below admits states that describe
	// the trading DAY — a break, an auction, a closed session — and a venue
	// answering with one of those about a frozen instrument silently withdrew
	// the catalogue's "not available", putting a 2022 print back into the sum at
	// full value. A disjunction cannot do that. What it costs is the mirror
	// case: paper whose catalogue snapshot was taken while everything was shut
	// counts as refused until the snapshot rolls over — and refusal alone does
	// nothing, because the print still has to be older than noTradeHorizon.
	// Erring towards holding paper OUT of a total is the direction this project
	// chose: a number that overstates is worse than one that discloses a gap.
	if !tradingModes[inst.TradingStatus] {
		return true
	}
	return status.FIGI != "" && !tradingModes[status.TradingStatus]
}

// tradingModes are the statuses under which the venue is running a market for an
// instrument, or is between sessions of one. Auctions, breaks and a closed
// session all belong here: they say where the trading day is, not whether the
// paper has a market. Anything outside this set — a refusal, an unspecified
// status, a member added after this was written — is read as a refusal.
var tradingModes = map[string]bool{
	StatusNormalTrading:              true,
	StatusDealerNormalTrading:        true,
	StatusOpeningPeriod:              true,
	StatusClosingPeriod:              true,
	StatusBreakInTrading:             true,
	StatusDealerBreakInTrading:       true,
	StatusOpeningAuction:             true,
	StatusClosingAuction:             true,
	StatusDarkPoolAuction:            true,
	StatusDiscreteAuction:            true,
	StatusClosingAuctionPriceTrading: true,
	StatusSessionAssigned:            true,
	StatusSessionClose:               true,
	StatusSessionOpen:                true,
}

// provenanceOf says what produced this number.
//
// A dealer quote is an appraisal: a market maker stated it, no trade made it.
// A halted instrument's last price is still a trade, just an old one, and
// calling that an appraisal would misdescribe it — its problem is age, which the
// freshness axis reports. The same goes for the last print before a session
// closed, which is why this reads the price type and nothing about the clock.
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
