// Package quoting answers one question about an asset: what is it worth per
// token in a given quote currency, and which stored price row says so?
//
// It exists because two surfaces asked it independently. The portfolio total
// (portfolio.unitPrice) and the heatmap (analytics.assetPricing) each resolved a
// quote, each applied the market-depth gate and each classified the failure,
// from two copies of the same code. Ф0 already paid for that shape once: способ
// #6 was the frontend recomputing a total by its own rules, and the rule that
// came out of it was «у числа один автор» — applied to the frontend, and not
// between two backend services. TestTotalAndHeatmapAgree was written to make a
// divergence loud; this package is what removes the way to create one.
//
// It is a sibling of pricefresh and marketdepth: a small package carrying one
// pricing rule, reading a narrow port rather than a service client.
package quoting

import (
	"context"
	"log/slog"
	"math"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/marketdepth"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/shopspring/decimal"
)

// PriceReader is the one thing this package needs from market data: the latest
// stored price of an asset, either in a named base or in whatever base it was
// last quoted in. Both *marketdata.Handler (monolith) and
// apiv1connect.MarketDataServiceClient (microservice) satisfy it as-is, which is
// why the callers pass their own client straight in.
type PriceReader interface {
	GetLatestPrice(context.Context, *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error)
}

// Quote is one usable way to express an asset in the quote currency: the price
// row it rests on, the rate converting that row's base, the per-token value that
// falls out, and the base itself — the heatmap reads change in the pair the
// asset actually trades in.
type Quote struct {
	Unit   decimal.Decimal
	Row    *apiv1.Price
	Rate   decimal.Decimal
	BaseID string
	// ValueBase is the price in its own base, before conversion; change is
	// computed there so a moving cross rate does not read as a moving asset.
	// The portfolio total ignores it — two fields are cheaper than two
	// implementations.
	ValueBase decimal.Decimal
}

// Candidates collects every way this asset can be expressed in quoteAssetID: its
// own freshest print converted from whatever pair it trades in, and a price
// quoted straight in the quote asset. They are the same row whenever the
// freshest print is already in the quote asset, and then there is only one
// candidate and nothing to choose between.
//
// When nothing comes back, the second return says which absence it is. An asset
// nobody has priced (NoQuote) and one priced in a base this valuation cannot
// convert from (NoCrossRate) look identical in a total and ask for opposite
// work: coverage for the asset in the first case, a single missing rate in the
// second. Collapsing them hid a live failure for months — see NoCrossRate.
func Candidates(ctx context.Context, r PriceReader, log *slog.Logger, assetID, quoteAssetID string) ([]Quote, Outcome, error) {
	one := decimal.NewFromInt(1)
	var out []Quote
	missing := NoQuote

	baseID, value, row, ok, err := LatestAnyBase(ctx, r, log, assetID)
	if err != nil {
		return nil, NoQuote, err
	}
	direct := ok && baseID == quoteAssetID
	switch {
	case direct:
		out = append(out, Quote{Unit: value, Row: row, Rate: one, BaseID: baseID, ValueBase: value})
	case ok:
		rate, hasRate, err := CrossRate(ctx, r, log, baseID, quoteAssetID)
		if err != nil {
			return nil, NoQuote, err
		}
		if hasRate {
			out = append(out, Quote{Unit: value.Mul(rate), Row: row, Rate: rate, BaseID: baseID, ValueBase: value})
		} else {
			// The asset IS quoted; only the conversion is missing. Provisional:
			// a price quoted straight in the quote asset, found below, still
			// prices it and clears this.
			missing = NoCrossRate
		}
	}

	if !direct {
		unit, row, ok, err := RealPrice(ctx, r, log, assetID, quoteAssetID)
		if err != nil {
			return nil, NoQuote, err
		}
		if ok {
			out = append(out, Quote{Unit: unit, Row: row, Rate: one, BaseID: quoteAssetID, ValueBase: unit})
		}
	}
	if len(out) > 0 {
		return out, Priced, nil
	}
	return nil, missing, nil
}

// Freshest picks the most recently observed candidate. A quote with no timestamp
// sorts oldest: it cannot claim currency it never stated.
//
// That is a correction, not a preference. Resolution used to be ordered by path —
// a direct quote first, a cross only if no direct one existed — so one stale
// direct row shadowed every fresher row the catalogue held, from any source,
// permanently. Prod 2026-08-14 is the proof: Binance wrote BTC/USDT hourly and
// current while CoinGecko's BTC/USD had been frozen since the 11th by a 429, and
// the total and the map both showed the frozen number.
//
// Panics on an empty slice; callers check Candidates for emptiness first, which
// is the unpriced case and has its own answer.
func Freshest(candidates []Quote) Quote {
	best := candidates[0]
	for _, c := range candidates[1:] {
		if pricefresh.QuotedAt(c.Row).After(pricefresh.QuotedAt(best.Row)) {
			best = c
		}
	}
	return best
}

// AnyThin reports whether any candidate fails the market-depth gate.
//
// A quote that survives selection still has to have a market behind it: MNEP
// priced $4,175 of airdropped tokens off a $40k/day market until this gate
// existed. It reads EVERY candidate that reports a volume, not only the one
// whose number is used, because the freshest row is not always the one carrying
// the evidence — Binance reports no volume at all, and letting its row displace
// a CoinGecko row that says "$40k a day" would disarm ADR-009 by accident. The
// rate is applied per candidate, since volume is denominated in that
// candidate's own base.
func AnyThin(candidates []Quote) (Quote, bool) {
	for _, c := range candidates {
		if marketdepth.Thin(c.Row, c.Rate) {
			return c, true
		}
	}
	return Quote{}, false
}

// CrossRate returns how many units of quoteID one unit of baseID is worth, using
// a direct baseID/quoteID price or, failing that, the inverse quoteID/baseID
// price.
//
// The price rows are discarded on purpose: a currency pair is not subject to the
// market-depth gate, which applies to the asset's own quote. Judging the rate
// would take down everything priced through it.
func CrossRate(ctx context.Context, r PriceReader, log *slog.Logger, baseID, quoteID string) (decimal.Decimal, bool, error) {
	if rate, _, ok, err := RealPrice(ctx, r, log, baseID, quoteID); err != nil || ok {
		return rate, ok, err
	}
	inv, _, ok, err := RealPrice(ctx, r, log, quoteID, baseID)
	if err != nil || !ok || inv.IsZero() {
		return decimal.Zero, false, err
	}
	return decimal.NewFromInt(1).Div(inv), true, nil
}

// LatestAnyBase returns the asset's most recent price regardless of base, as the
// base asset ID, the real-unit value and the price row it came from. ok is false
// when no price exists.
func LatestAnyBase(ctx context.Context, r PriceReader, log *slog.Logger, assetID string) (string, decimal.Decimal, *apiv1.Price, bool, error) {
	resp, err := r.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, // BaseAssetId omitted → latest in any base
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return "", decimal.Zero, nil, false, nil
		}
		return "", decimal.Zero, nil, false, err
	}
	value, ok, err := RealUnit(resp.Msg, log, assetID)
	if err != nil || !ok {
		return "", decimal.Zero, nil, false, err
	}
	return resp.Msg.BaseAssetId, value, resp.Msg, true, nil
}

// RealPrice returns the latest price of assetID in baseID as a real-unit decimal
// (value = last / 10^decimals), together with the price row it was read from —
// callers need the market context on it, not just the number. ok is false when
// no price exists (NotFound) or the stored value is unparseable; a non-nil error
// is returned only for unexpected failures.
func RealPrice(ctx context.Context, r PriceReader, log *slog.Logger, assetID, baseID string) (decimal.Decimal, *apiv1.Price, bool, error) {
	resp, err := r.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, BaseAssetId: baseID,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return decimal.Zero, nil, false, nil
		}
		return decimal.Zero, nil, false, err
	}
	value, ok, err := RealUnit(resp.Msg, log, assetID)
	return value, resp.Msg, ok, err
}

// RealUnit converts a Price proto into a real-unit decimal (last / 10^decimals).
// ok is false when the stored value is unparseable (logged, not fatal).
func RealUnit(p *apiv1.Price, log *slog.Logger, assetID string) (decimal.Decimal, bool, error) {
	last, err := decimal.NewFromString(p.Last)
	if err != nil {
		if log != nil {
			log.Warn("skip price with unparseable last",
				"asset_id", assetID, "base_asset_id", p.BaseAssetId, "last", p.Last, "error", err)
		}
		return decimal.Zero, false, nil
	}
	return last.Shift(-decI32(p.Decimals)), true, nil
}

// decI32 clamps stored decimals to int32. They arrive from external metadata and
// are tiny in practice (<= ~36), but the wire type permits values that would
// overflow when narrowed, which gosec flags as G115. Clamping rather than
// erroring is safe: an absurd decimals value already yields meaningless amounts
// downstream, so the clamp only ever rewrites input that was broken to begin
// with.
func decI32(d uint32) int32 {
	if d > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(d)
}
