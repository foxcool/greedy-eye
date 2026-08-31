package tinvest

import (
	"context"
	"fmt"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// quantityDecimals is the scale positions are reported and stored at.
//
// Nine, because that is what a Quotation carries: units plus a nano field. It
// is a property of the wire format, not of the instrument — inferring it from
// the instrument type ("shares are whole numbers") is how a fractional lot
// becomes a rounding error nobody sees.
const quantityDecimals = 9

// BrokerSyncer reads one broker account's positions.
type BrokerSyncer struct {
	client    *Client
	catalog   *catalog
	accountID string
}

// NewBrokerSyncer binds a client to ONE broker account. The account id is the
// broker's own, carried in our account's data["broker_account_id"].
func NewBrokerSyncer(c *Client, brokerAccountID string) *BrokerSyncer {
	return &BrokerSyncer{client: c, catalog: newCatalog(c), accountID: brokerAccountID}
}

// SyncBroker implements entity.BrokerSyncer.
func (s *BrokerSyncer) SyncBroker(ctx context.Context) ([]entity.BrokerPosition, entity.BrokerSkips, error) {
	var skips entity.BrokerSkips

	portfolio, err := s.client.Portfolio(ctx, s.accountID)
	if err != nil {
		return nil, skips, err
	}

	out := make([]entity.BrokerPosition, 0, len(portfolio.Positions))
	for _, pos := range portfolio.Positions {
		mapped, err := s.position(ctx, pos, &skips)
		if err != nil {
			skips.Unparsable++
			continue
		}
		out = append(out, mapped...)
	}
	return out, skips, nil
}

// position turns one broker line into the holdings it represents — two of them
// when the broker has blocked part of it.
func (s *BrokerSyncer) position(ctx context.Context, pos PortfolioPosition, skips *entity.BrokerSkips) ([]entity.BrokerPosition, error) {
	quantity, ok := pos.Quantity.Decimal()
	if !ok {
		return nil, fmt.Errorf("tinvest: position %s: unreadable quantity", pos.FIGI)
	}
	if quantity.IsZero() {
		return nil, nil
	}

	base := entity.BrokerPosition{
		Symbol:   strings.ToUpper(strings.TrimSpace(pos.Ticker)),
		Currency: strings.ToLower(pos.CurrentPrice.Currency),
		Decimals: quantityDecimals,
	}

	if pos.InstrumentType == InstrumentTypeCurrency {
		// Cash is the one exception to identity-by-FIGI, and deliberately so.
		// The capture has figi=USD800UTSTOM against ticker=USD000UTSTOM:
		// binding by either would tie dollars to a settlement instrument rather
		// than to the currency the rest of the system already prices.
		//
		// AND NOT FROM CurrentPrice EITHER. On a cash line that field says what
		// the position is WORTH IN, not what is held: 0.88 dollars is priced in
		// roubles, so currency reads "rub". Taking the code from there books
		// dollars as roubles — an eighty-fivefold error, and the same shape as
		// the hundredfold one this adapter already bought on quotes.
		code := currencyCodeOf(pos.Ticker)
		if code == "" {
			return nil, fmt.Errorf("tinvest: cash position %s: cannot read a currency from ticker %q", pos.FIGI, pos.Ticker)
		}
		base.Ref = ""
		base.Symbol = code
		base.Name = code
		base.Type = entity.AssetTypeForex
		base.Market = entity.DefaultMarket(entity.AssetTypeForex)
		return splitByLiquidity(base, quantity, pos), nil
	}

	typ, ok := instrumentAssetType(pos.InstrumentType)
	if !ok {
		skips.UnknownInstrument++
		return nil, nil
	}
	base.Ref = pos.FIGI
	base.Type = typ

	inst, found, err := s.catalog.instrument(ctx, pos.FIGI)
	if err != nil {
		return nil, err
	}
	switch {
	case found:
		base.Market = MarketOf(inst)
		if base.Symbol == "" {
			base.Symbol = strings.ToUpper(strings.TrimSpace(inst.Ticker))
		}
		base.Name = inst.Name
		if base.Market == "" {
			// The catalogue knows the instrument but settles it somewhere this
			// system has no market for. Guessing here would be worse than the
			// unknown case: we have the venue and would be overriding it.
			skips.UnknownMarket++
			return nil, nil
		}
	default:
		// Not in the catalogue: delisted paper, or a type this adapter does not
		// load. The board the position was reported on is the best remaining
		// evidence — it is what the broker actually said, where the currency is
		// only what the row is priced in. Recorded in DefaultedMarket so the
		// guess is never silent; repair path is personal-uusf.
		base.Market = marketOfClassCode(pos.ClassCode)
		if base.Market == "" {
			base.Market = marketOfCurrency(base.Currency)
		}
		if base.Market == "" {
			skips.UnknownInstrument++
			return nil, nil
		}
		if base.Name == "" {
			base.Name = base.Symbol
		}
		skips.DefaultedMarket++
	}

	if base.Symbol == "" {
		return nil, fmt.Errorf("tinvest: position %s: no symbol", pos.FIGI)
	}
	return splitByLiquidity(base, quantity, pos), nil
}

// splitByLiquidity partitions a position into the part that can be sold and the
// part the broker has blocked.
//
// Both shapes the API can report are handled, because they answer different
// questions and the response populates them independently: BlockedLots says HOW
// MUCH is restricted, Blocked says THAT the line is. A quantity split across
// two rows must still sum to what was reported — overlapping pools are the
// doubling trap the Cosmos and Substrate adapters were both bitten by.
func splitByLiquidity(base entity.BrokerPosition, quantity decimal.Decimal, pos PortfolioPosition) []entity.BrokerPosition {
	locked := decimal.Zero
	if blocked, ok := pos.BlockedLots.Decimal(); ok && blocked.IsPositive() {
		locked = decimal.Min(blocked, quantity)
	} else if pos.Blocked {
		locked = quantity
	}

	liquid := quantity.Sub(locked)
	out := make([]entity.BrokerPosition, 0, 2)
	if liquid.IsPositive() {
		row := base
		row.Liquidity = entity.LiquidityLiquid
		row.Amount = scaled(liquid)
		out = append(out, row)
	}
	if locked.IsPositive() {
		row := base
		row.Liquidity = entity.LiquidityLocked
		row.Amount = scaled(locked)
		out = append(out, row)
	}
	return out
}

// scaled renders a quantity as the raw integer string the write path expects.
func scaled(v decimal.Decimal) string {
	return v.Shift(quantityDecimals).Truncate(0).String()
}

// instrumentAssetType maps the broker's instrument type onto ours. An unknown
// type is not guessed: a future or an option has no meaning in a model whose
// holdings are all long positions with amount > 0.
func instrumentAssetType(t string) (entity.AssetType, bool) {
	switch t {
	case InstrumentTypeShare:
		return entity.AssetTypeStock, true
	case InstrumentTypeEtf:
		return entity.AssetTypeFund, true
	case InstrumentTypeBond:
		return entity.AssetTypeBond, true
	default:
		return entity.AssetTypeUnspecified, false
	}
}

// currencyCodeOf reads which currency a cash line holds.
//
// From the TICKER, because that is the only field that says it: the capture has
// USD000UTSTOM, RUB000UTSTOM and EUR_RUB__TOM_CETS, all of which open with the
// ISO code. The figi does not (USD800UTSTOM for the first, BBG0013HJJ31 for the
// third) and currentPrice says what the line is priced in, not what it is.
func currencyCodeOf(ticker string) string {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if len(t) < 3 {
		return ""
	}
	code := t[:3]
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return code
}

// marketOfClassCode reads a venue off the board a position was reported on.
//
// Only a fallback. RealExchange from the instrument catalogue is the field that
// names a venue outright, which is why the price path uses it; a board code
// varies by segment and by session. But for an instrument the catalogue does
// not carry, this is what the broker did say, and it beats inferring a venue
// from the currency: the capture has DE0005190003 on SPBXM_OTC quoted in euros,
// which no currency rule would place correctly.
//
// The _OTC suffix marks an over-the-counter board of the same venue, not a
// different one.
func marketOfClassCode(classCode string) string {
	code := strings.ToUpper(strings.TrimSpace(classCode))
	code = strings.TrimSuffix(code, "_OTC")
	switch {
	case strings.HasPrefix(code, "SPB"):
		return MarketSPBEX
	case strings.HasPrefix(code, "TQ"), strings.HasPrefix(code, "FINEX"):
		return MarketMOEX
	default:
		return ""
	}
}

// marketOfCurrency is the last fallback, when neither the catalogue nor the
// board says anything. Bounded on purpose: a rouble line is a Moscow listing
// and a dollar line a Saint Petersburg one, which holds for what a Russian
// broker sells; anything else declines rather than reaching for a third answer.
func marketOfCurrency(currency string) string {
	switch strings.ToLower(currency) {
	case "rub":
		return MarketMOEX
	case "usd":
		return MarketSPBEX
	default:
		return ""
	}
}
