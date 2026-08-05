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
	byMarket := map[Market][]*entity.Asset{}
	for _, a := range assets {
		market, ok := issMarket(a)
		if !ok {
			continue
		}
		byMarket[market] = append(byMarket[market], a)
	}
	if len(byMarket) == 0 {
		return nil, nil
	}

	var result []entity.StoredPrice
	for market, group := range byMarket {
		prices, err := p.fetchMarket(ctx, market, group)
		if err != nil {
			// Partial by design: bonds failing must not cost the portfolio its
			// share prices, and the acceptance criterion for this adapter is
			// that one bad ticker does not fail the batch.
			p.log.Warn("moex market lookup failed", "market", market, "error", err)
			continue
		}
		result = append(result, prices...)
	}
	return result, nil
}

// fetchMarket prices one ISS market in batches.
func (p *Provider) fetchMarket(ctx context.Context, market Market, assets []*entity.Asset) ([]entity.StoredPrice, error) {
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

		quotes, err := p.client.Quotes(ctx, market, secIDs[start:end])
		if err != nil {
			// Keep going: a failed batch costs its own tickers, not the ones
			// already collected.
			lastErr = err
			p.log.Warn("moex batch failed", "market", market, "error", err)
			continue
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

	value, at, traded := lastOrPrevious(q)
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
		Interval:  interval,
		Decimals:  priceDecimals,
		Last:      value.Decimal.Shift(int32(priceDecimals)).Round(0),
		Timestamp: at,
	}

	// Turnover is only reported alongside a price it actually describes. On a
	// previous close today's zero says nothing about that print's market, and
	// storing it would read as "traded, and nobody wanted it" — which is how
	// every MOEX position would be gated as a thin market every weekend.
	if traded && q.ValToday.Valid && q.ValToday.Decimal.IsPositive() {
		price.Volume = decimal.NullDecimal{
			Decimal: q.ValToday.Decimal.Shift(int32(priceDecimals)).Round(0),
			Valid:   true,
		}
	}

	if !price.Last.IsPositive() {
		return entity.StoredPrice{}, false
	}
	return price, true
}

// lastOrPrevious picks the price to publish and the moment it belongs to.
// traded reports whether the value came from the current session.
func lastOrPrevious(q Quote) (value decimal.NullDecimal, at time.Time, traded bool) {
	if q.Last.Valid && q.Last.Decimal.IsPositive() {
		return q.Last, q.SysTime, true
	}
	// Outside trading hours the previous close is the honest current price, but
	// it is dated to the session it came from rather than to the sweep. A
	// Friday close read on Sunday is two days old and says so.
	return q.Prev, q.PrevDate, false
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
	if ap, bp := rank(a.Board), rank(b.Board); ap != bp {
		return ap < bp
	}
	// Equally traded and equally ranked: a board carrying any price at all
	// beats one carrying none.
	return hasPrice(a) && !hasPrice(b)
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

// issMarket reports which ISS market prices this asset, and whether this
// provider speaks for it at all.
func issMarket(a *entity.Asset) (Market, bool) {
	if a == nil || entity.NormalizeMarket(a.Market) != MarketName {
		return "", false
	}
	switch a.Type {
	case entity.AssetTypeStock, entity.AssetTypeFund:
		return MarketShares, true
	case entity.AssetTypeBond:
		return MarketBonds, true
	default:
		return "", false
	}
}

func isRouble(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), issCurrency)
}
