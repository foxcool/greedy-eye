package moex

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

	// quoteSymbol is what MOEX quotes in. This is the first provider whose base
	// is neither USD nor USDT, which is why the RUB/USD leg had to exist first
	// (personal-gip.3): without it a rouble-quoted instrument resolves to a
	// price and still contributes nothing to a USD total.
	quoteSymbol = "RUB"

	// issCurrency is how ISS spells the rouble in CURRENCYID and FACEUNIT.
	issCurrency = "SUR"

	// batchSize caps how many tickers go into one request's securities= list.
	// ISS accepts a long list; a URL that grows without bound does not.
	batchSize = 100
)

// MarketName is the value of assets.market this provider speaks for.
const MarketName = "moex"

// boardPriority ranks the main trading boards, used only to break a tie between
// boards that are equally untraded today. The order is MOEX's own primary
// boards per instrument class: shares, exchange-traded funds, closed-end funds,
// then the two bond boards (federal and corporate).
var boardPriority = map[string]int{
	"TQBR": 0, "TQTF": 1, "TQIF": 2, "TQOB": 3, "TQCB": 4,
}

const unrankedBoard = 100

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client *Client
	log    *slog.Logger
}

// NewProvider wraps a *Client as a MOEX price provider.
func NewProvider(c *Client) *Provider {
	return &Provider{client: c, log: slog.Default()}
}

// BaseAssetSymbol returns the ticker MOEX quotes in.
func (p *Provider) BaseAssetSymbol() string { return quoteSymbol }

// BaseAssetType reports that the quote currency is fiat.
func (p *Provider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

// AssetBudget reports no per-asset allowance: one request covers a hundred
// tickers of a market, and the API is free and keyless, so there is no plan to
// divide. Returning ok=false leaves the sweep uncapped by volume, which is the
// truthful answer rather than a number invented to look prudent.
func (p *Provider) AssetBudget(time.Time, time.Duration) (int, bool) { return 0, false }

// FetchPrices prices MOEX-listed instruments in roubles.
//
// Instruments are selected by market, not by ticker shape: assets.market is the
// listing venue, and a three-letter ticker is not evidence of anything. Type
// then picks which ISS market to ask — shares and funds share one, bonds have
// their own.
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	byClass := map[entity.AssetType][]*entity.Asset{}
	for _, a := range assets {
		if !p.speaksFor(a) {
			continue
		}
		byClass[a.Type] = append(byClass[a.Type], a)
	}
	if len(byClass) == 0 {
		return nil, nil
	}

	var result []entity.StoredPrice
	for class, group := range byClass {
		prices, err := p.fetchClass(ctx, venuesFor(class), group)
		if err != nil {
			// Partial by design: bonds failing must not cost the portfolio its
			// share prices, and the acceptance criterion for this adapter is
			// that one bad ticker does not fail the batch.
			p.log.Warn("moex lookup failed", "asset_type", class, "error", err)
			continue
		}
		result = append(result, prices...)
	}
	return result, nil
}

// fetchClass prices one instrument class across every venue that can carry it.
//
// The venues are asked in full and their rows pooled before a winner is picked,
// because "which venue has the real price" is exactly the question bestBoards
// already answers with turnover — asking the exchange floor first and stopping
// there would reinstate the assumption this task removed.
func (p *Provider) fetchClass(ctx context.Context, venues []Venue, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	bySecID := make(map[string]*entity.Asset, len(assets))
	secIDs := make([]string, 0, len(assets))
	for _, a := range assets {
		secID := entity.NormalizeSymbol(a.Symbol)
		if _, seen := bySecID[secID]; seen {
			continue
		}
		bySecID[secID] = a
		secIDs = append(secIDs, secID)
	}

	var result []entity.StoredPrice
	var lastErr error
	for start := 0; start < len(secIDs); start += batchSize {
		end := min(start+batchSize, len(secIDs))
		batch := secIDs[start:end]

		var quotes []Quote
		for _, venue := range venues {
			got, err := p.client.Quotes(ctx, venue, batch)
			if err != nil {
				// Keep going: a venue that fails costs its own rows, not the ones
				// another venue already answered with. An OTC outage must not take
				// the main board's prices down with it.
				lastErr = err
				p.log.Warn("moex venue batch failed", "venue", venue, "error", err)
				continue
			}
			quotes = append(quotes, got...)
		}

		for secID, quote := range bestBoards(quotes) {
			asset, ok := bySecID[secID]
			if !ok {
				continue
			}
			price, ok := p.storedPrice(asset, quote)
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

// storedPrice turns one board's quote into a price row, or reports that the
// quote cannot honestly become one.
func (p *Provider) storedPrice(asset *entity.Asset, q Quote) (entity.StoredPrice, bool) {
	// A quote in another currency would be published as roubles by the base
	// asset this provider declares. Eurobonds with a USD nominal are the real
	// case, and the error is a hundredfold, not a rounding one.
	if !isRouble(q.Currency) {
		p.log.Warn("moex quote is not in roubles, asset left unpriced",
			"sec_id", q.SecID, "board", q.Board, "currency", q.Currency)
		return entity.StoredPrice{}, false
	}

	b := basis(q)
	value, at := b.value, b.at
	if !value.Valid || !value.Decimal.IsPositive() {
		return entity.StoredPrice{}, false
	}

	if asset.Type == entity.AssetTypeBond {
		converted, ok := bondPrice(value.Decimal, q)
		if !ok {
			p.log.Warn("moex bond has no rouble nominal, asset left unpriced",
				"sec_id", q.SecID, "face_unit", q.FaceUnit)
			return entity.StoredPrice{}, false
		}
		value.Decimal = converted
	}

	if at.IsZero() {
		at = time.Now()
	}

	price := entity.StoredPrice{
		SourceID: sourceID,
		AssetID:  asset.ID,
		// BaseAssetID is intentionally empty — resolved by FetchExternalPrices.
		Interval:   interval,
		Decimals:   priceDecimals,
		Last:       value.Decimal.Shift(int32(priceDecimals)).Round(0),
		Timestamp:  at,
		Provenance: b.provenance,
	}

	// Turnover is only reported alongside a price it actually describes. On a
	// previous close today's zero says nothing about that print's market, and
	// storing it would read as "traded, and nobody wanted it" — which is how
	// every MOEX position would be gated as a thin market every weekend.
	//
	// An appraised close is the one case where zero IS the measurement: the
	// session that produced the number had no trades, and ADR-009's gate is
	// exactly the rule that such a print must not be valued at face. Reporting it
	// keeps the number in the catalogue and out of the total.
	if turnover := b.turnover; turnover.Valid {
		price.Volume = decimal.NullDecimal{
			Decimal: turnover.Decimal.Shift(int32(priceDecimals)).Round(0),
			Valid:   true,
		}
	}

	if !price.Last.IsPositive() {
		return entity.StoredPrice{}, false
	}
	return price, true
}

// quoteBasis is the price to publish, when it happened, and what stands behind
// it.
type quoteBasis struct {
	value      decimal.NullDecimal
	at         time.Time
	provenance entity.PriceProvenance
	// turnover is published only when it describes the same session as value.
	// Invalid means "no claim", which is not the same as a turnover of zero.
	turnover decimal.NullDecimal
}

// basis picks the price to publish and reads what produced it.
//
// Three cases, and the third is why this function exists:
//
//   - A trade today. LAST is a print somebody paid; today's turnover describes
//     that same session, so it travels with it.
//
//   - A previous session that traded. ISS publishes PREVWAPRICE — a
//     volume-weighted average — and a weighted average of no trades does not
//     exist, so its presence is the evidence. Today's zero turnover says nothing
//     about that session and is not published beside it.
//
//   - A previous session that did NOT trade, for which the exchange still
//     published a recognised close. This is the case that made FXGD reachable
//     only at 93.55 ₽ against a real last traded price of 37 ₽ — the same
//     fields, the same shape, 2.5x apart. The number is real and worth keeping;
//     calling it a market price is not. It is marked appraised and carries an
//     explicit zero turnover, which is a measurement of that session and not an
//     assumption about this one.
func basis(q Quote) quoteBasis {
	if q.Last.Valid && q.Last.Decimal.IsPositive() {
		b := quoteBasis{value: q.Last, at: q.SysTime, provenance: entity.PriceProvenanceTraded}
		if q.ValToday.Valid && q.ValToday.Decimal.IsPositive() {
			b.turnover = q.ValToday
		}
		return b
	}

	// Outside trading hours the previous close is the honest current price, but
	// it is dated to the session it came from rather than to the sweep. A
	// Friday close read on Sunday is two days old and says so.
	b := quoteBasis{value: q.Prev, at: q.PrevDate}
	switch {
	case q.PrevWAP.Valid && q.PrevWAP.Decimal.IsPositive():
		b.provenance = entity.PriceProvenanceTraded
	case q.PrevLegalClose.Valid && q.PrevLegalClose.Decimal.IsPositive():
		b.provenance = entity.PriceProvenanceAppraised
		b.turnover = decimal.NullDecimal{Decimal: decimal.Zero, Valid: true}
	default:
		// Boards that publish neither a weighted average nor a recognised close
		// (SPEQ carries a previous price and neither, measured 2026-08-09) leave
		// us without grounds for either claim. Unknown is the honest answer, and
		// it behaves exactly as this adapter did before provenance existed.
		b.provenance = entity.PriceProvenanceUnknown
	}
	return b
}

// bondPrice converts a bond's quote — a percentage of nominal — into money.
//
// Accrued interest is deliberately excluded: it is real value the holder is
// owed, so leaving it out understates the position by at most one coupon, and
// understating is the safe direction. Modelling the coupon properly is
// personal-b7l, and inventing a dirty price here would put a second author on a
// number that task has to define.
func bondPrice(percent decimal.Decimal, q Quote) (decimal.Decimal, bool) {
	if !isRouble(q.FaceUnit) || !q.FaceValue.Valid || !q.FaceValue.Decimal.IsPositive() {
		return decimal.Zero, false
	}
	return percent.Mul(q.FaceValue.Decimal).Div(decimal.NewFromInt(100)), true
}

// bestBoards reduces ISS's row per (security, board) to one row per security.
//
// The board that traded most today wins, because that is where the price was
// actually made. Only when nothing traded anywhere does the ranking of main
// boards decide, and a board nobody ranks loses to one that is ranked.
func bestBoards(quotes []Quote) map[string]Quote {
	best := make(map[string]Quote, len(quotes))
	for _, q := range quotes {
		current, seen := best[q.SecID]
		if !seen || better(q, current) {
			best[q.SecID] = q
		}
	}
	return best
}

// better reports whether a should replace b as the security's quote.
func better(a, b Quote) bool {
	av, bv := turnover(a), turnover(b)
	if !av.Equal(bv) {
		return av.GreaterThan(bv)
	}
	// Nothing traded today on either. A print somebody paid for outranks one the
	// exchange wrote down, whichever board each came from — otherwise a main-board
	// appraisal would beat the over-the-counter trade that is the real last price,
	// which is the whole failure this ranking exists to prevent.
	if at, bt := traded(a), traded(b); at != bt {
		return at
	}
	if ap, bp := rank(a.Board), rank(b.Board); ap != bp {
		return ap < bp
	}
	// Equally traded and equally ranked: a board carrying any price at all
	// beats one carrying none.
	return hasPrice(a) && !hasPrice(b)
}

// traded reports whether a trade — today's or the previous session's — stands
// behind this row's price.
func traded(q Quote) bool {
	return basis(q).provenance == entity.PriceProvenanceTraded
}

func turnover(q Quote) decimal.Decimal {
	if q.ValToday.Valid {
		return q.ValToday.Decimal
	}
	return decimal.Zero
}

func rank(board string) int {
	if p, ok := boardPriority[board]; ok {
		return p
	}
	return unrankedBoard
}

func hasPrice(q Quote) bool {
	return (q.Last.Valid && q.Last.Decimal.IsPositive()) ||
		(q.Prev.Valid && q.Prev.Decimal.IsPositive())
}

// Asked reports which of these assets MOEX is actually asked about.
//
// The sweep selects per source but cannot know what a source covers, so MOEX is
// handed the whole due list — most of which is crypto it has never quoted.
// Recording those as misses filed MOEX's silence against assets it does not
// cover: measured on dev, 575 miss rows for crypto assets, driving them to the
// week-long back-off ceiling and freezing the queue for the sources that DO
// cover them.
func (p *Provider) Asked(assets []*entity.Asset) []*entity.Asset {
	out := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if p.speaksFor(a) {
			out = append(out, a)
		}
	}
	return out
}

// speaksFor reports whether this provider prices the asset at all.
//
// Selection is by market, not by ticker shape: assets.market is the listing
// venue, and a three-letter ticker is not evidence of anything.
func (p *Provider) speaksFor(a *entity.Asset) bool {
	if a == nil || entity.NormalizeMarket(a.Market) != MarketName {
		return false
	}
	return len(venuesFor(a.Type)) > 0
}

// venuesFor lists the (engine, market) pairs that can carry an instrument class.
//
// Shares and funds are asked of the exchange floor AND of the two over-the-counter
// markets, because admission to one says nothing about the others: FXGD was absent
// from engine "stock" while trading 24-77 times a day on MTQR under engine "otc".
//
// Bonds stay on the exchange floor alone. OTC bond markets exist, but nothing in
// this portfolio has been measured against them, and a venue added on the theory
// that it might help is a venue nobody has read a row from.
func venuesFor(t entity.AssetType) []Venue {
	switch t {
	case entity.AssetTypeStock, entity.AssetTypeFund:
		return []Venue{VenueStockShares, VenueOTCShares, VenueOTCSharesNDM}
	case entity.AssetTypeBond:
		return []Venue{VenueStockBonds}
	default:
		return nil
	}
}

func isRouble(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), issCurrency)
}
