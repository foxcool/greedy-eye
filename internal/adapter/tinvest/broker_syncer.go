package tinvest

import (
	"context"
	"fmt"
	"log/slog"
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

// settlementCurrencies are the codes a cash line may name.
//
// An allowlist rather than "any three letters", because this broker files
// precious metals under the currency instrument type: GLDRUB_TOM and
// SLVRUB_TOM would otherwise mint assets called GLD and SLV, which no FX source
// quotes and nothing can later re-bind — cash is the one path that stores no
// provider ref. A metal is a real holding and deserves a real asset; until it
// has one it is counted, not invented.
var settlementCurrencies = map[string]bool{
	"RUB": true, "USD": true, "EUR": true, "CNY": true,
	"HKD": true, "GBP": true, "CHF": true, "JPY": true,
	"TRY": true, "KZT": true, "BYN": true, "AMD": true,
}

// BrokerSyncer reads one broker account's positions.
type BrokerSyncer struct {
	client    *Client
	catalog   *catalog
	accountID string
	log       *slog.Logger
}

// NewBrokerSyncer binds a client to ONE broker account. The account id is the
// broker's own, carried in our account's data["broker_account_id"].
func NewBrokerSyncer(c *Client, brokerAccountID string) *BrokerSyncer {
	return &BrokerSyncer{
		client:    c,
		catalog:   newCatalog(c),
		accountID: brokerAccountID,
		log:       slog.Default(),
	}
}

// SyncBroker implements entity.BrokerSyncer.
func (s *BrokerSyncer) SyncBroker(ctx context.Context) ([]entity.BrokerPosition, entity.BrokerSkips, error) {
	var skips entity.BrokerSkips

	portfolio, err := s.client.Portfolio(ctx, s.accountID)
	if err != nil {
		return nil, skips, err
	}

	// The catalogue is loaded ONCE, before any position is looked at, and its
	// failure fails the whole sync.
	//
	// Reached lazily from inside the loop instead, an expired token or a 429
	// from the shared rate budget arrives as a per-position error and is
	// counted as an unreadable position — so an outage returns a plausible
	// portfolio of nothing but cash, with a nil error. The caller's contract
	// (upsertSyncedBalances) treats a complete snapshot as licence to zero
	// everything it no longer contains.
	if err := s.catalog.warm(ctx); err != nil {
		return nil, skips, err
	}

	out := make([]entity.BrokerPosition, 0, len(portfolio.Positions))
	for _, pos := range portfolio.Positions {
		mapped, err := s.position(pos, &skips)
		if err != nil {
			// Counted AND said out loud: the count tells an operator how much
			// is missing, the log tells them which row and why.
			skips.Unparsable++
			// The broker account id is deliberately NOT logged: it is the real
			// account number, and pairing it with a portfolio's instruments in
			// a log aggregator puts more together than the database itself
			// holds. The figi is enough to find the row again.
			s.log.Warn("tinvest: position skipped",
				slog.String("figi", pos.FIGI),
				slog.String("ticker", pos.Ticker),
				slog.Any("error", err))
			continue
		}
		out = append(out, mapped...)
	}
	return out, skips, nil
}

// position turns one broker line into the holdings it represents.
func (s *BrokerSyncer) position(pos PortfolioPosition, skips *entity.BrokerSkips) ([]entity.BrokerPosition, error) {
	quantity, ok := pos.Quantity.Decimal()
	if !ok {
		return nil, fmt.Errorf("unreadable quantity")
	}
	if quantity.IsZero() {
		return nil, nil
	}
	if quantity.IsNegative() {
		// A short or a margin debt. Every holding in this model is a long
		// position, so there is no shape to write it in — and dropping it
		// silently would let a liability leave the books while the response
		// still reported itself as complete.
		return nil, fmt.Errorf("negative quantity %s: this model holds only long positions", quantity)
	}

	base := entity.BrokerPosition{
		Symbol:   strings.ToUpper(strings.TrimSpace(pos.Ticker)),
		Currency: strings.ToLower(pos.CurrentPrice.Currency),
		Decimals: quantityDecimals,
	}

	if pos.InstrumentType == InstrumentTypeCurrency {
		return s.cash(base, quantity, pos)
	}

	typ, ok := instrumentAssetType(pos.InstrumentType)
	if !ok {
		skips.UnknownInstrument++
		return nil, nil
	}
	base.Ref = pos.FIGI
	base.Type = typ

	inst, found := s.catalog.known(pos.FIGI)
	lot := 0
	switch {
	case found:
		lot = inst.Lot
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
		// only what the row is priced in.
		market := marketOfClassCode(pos.ClassCode)
		if market == "" {
			market = marketOfCurrency(base.Currency)
		}
		if market == "" {
			skips.UnknownInstrument++
			return nil, nil
		}
		if base.Symbol == "" {
			// Checked BEFORE the guess is recorded: counting a defaulted market
			// for a row that is about to be refused would report a holding that
			// does not exist, and the caller would count the same position
			// again as unparsable.
			return nil, fmt.Errorf("no ticker, and the catalogue does not carry this instrument")
		}
		base.Market = market
		base.Name = base.Symbol
		// Recorded so the guess is never silent; repair path is personal-uusf.
		skips.DefaultedMarket++
	}

	if base.Symbol == "" {
		return nil, fmt.Errorf("no symbol")
	}
	return splitByLiquidity(base, quantity, pos, lot), nil
}

// cash maps a currency line onto the currency it actually holds.
func (s *BrokerSyncer) cash(base entity.BrokerPosition, quantity decimal.Decimal, pos PortfolioPosition) ([]entity.BrokerPosition, error) {
	// Cash is the one exception to identity-by-provider-ref, and deliberately
	// so. The capture has figi=USD800UTSTOM against ticker=USD000UTSTOM:
	// binding by either would tie dollars to a settlement instrument rather
	// than to the currency the rest of the system already prices.
	//
	// AND NOT FROM CurrentPrice EITHER. On a cash line that field says what the
	// position is WORTH IN, not what is held: 0.88 dollars is priced in
	// roubles, so currency reads "rub". Taking the code from there books
	// dollars as roubles — an eighty-fivefold error, and the same shape as the
	// hundredfold one this adapter already bought on quotes.
	code := currencyCodeOf(pos.Ticker)
	if code == "" {
		return nil, fmt.Errorf("ticker %q names no settlement currency this adapter knows", pos.Ticker)
	}
	base.Ref = ""
	base.Symbol = code
	base.Name = code
	base.Type = entity.AssetTypeForex
	base.Market = entity.DefaultMarket(entity.AssetTypeForex)
	// The row IS the currency, so that is also the currency it is denominated
	// in. Leaving the price's "rub" here would contradict the paragraph above
	// in the one field a consumer would read to value the amount.
	base.Currency = strings.ToLower(code)
	// A cash line is never partially blocked in any capture seen, and it has no
	// lot: money is not traded in round numbers of itself.
	return splitByLiquidity(base, quantity, pos, 1), nil
}

// splitByLiquidity partitions a position into the part that can be sold and the
// part the broker has blocked.
//
// BLOCKED LOTS ARE LOTS. quantity is in pieces, and the two differ by the
// instrument's lot size — the capture proves it in the same message: DASB
// reports quantity 10000 against quantityLots 1, GAZP 310 against 31, HYDR
// 20000 against 20. Subtracting one from the other directly, which this
// function did until a review caught it, reports 9999 of a fully blocked
// 10000-share holding as sellable.
//
// When the lot size is unknown — the instrument is not in the catalogue — a
// positive blockedLots locks the WHOLE line. Erring towards locked understates
// what can be sold; erring the other way tells someone money is reachable when
// it is not, and that is the answer runway is built on.
//
// The pools must not overlap and must sum to what the broker reported: an
// overlapping split is the doubling trap the Cosmos and Substrate adapters were
// both bitten by.
func splitByLiquidity(base entity.BrokerPosition, quantity decimal.Decimal, pos PortfolioPosition, lot int) []entity.BrokerPosition {
	locked := decimal.Zero
	if blockedLots, ok := pos.BlockedLots.Decimal(); ok && blockedLots.IsPositive() {
		if lot > 0 {
			locked = decimal.Min(blockedLots.Mul(decimal.NewFromInt(int64(lot))), quantity)
		} else {
			locked = quantity
		}
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
//
// The prefix is checked against settlementCurrencies rather than accepted for
// being three letters: this broker files metals under the same instrument type.
func currencyCodeOf(ticker string) string {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if len(t) < 3 {
		return ""
	}
	if code := t[:3]; settlementCurrencies[code] {
		return code
	}
	return ""
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
