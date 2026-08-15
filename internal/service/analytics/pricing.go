package analytics

import (
	"context"
	"math"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/marketdepth"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// assetPricing is everything the heatmap needs to know about one asset.
type assetPricing struct {
	unit      decimal.Decimal // current per-token price in the quote asset
	priced    bool            // false when there is no usable price → asset is skipped
	changePct float64         // signed % change over the window; 0 when history is missing
	label     string          // asset symbol, falling back to name, then ID
	// reason says why an unpriced asset stayed off the map; meaningless when
	// priced. NO_QUOTE is the default because "no price row anywhere" is the
	// outcome of every path that simply fails to find one.
	reason apiv1.UnpricedReason
	// quotedAt is when the price above was observed; zero when unpriced or when
	// the quote carried no time. It is the asset's OWN quote on the cross path
	// too — a stale conversion rate says nothing about whether this asset still
	// trades, the same reasoning that keeps the depth gate off the rate.
	quotedAt time.Time
}

// quote is one usable way to express an asset in the quote currency: the price
// row it rests on, the rate converting that row's base, the per-token value that
// falls out, and the base itself — the map reads change in the pair the asset
// actually trades in.
type quote struct {
	unit   decimal.Decimal
	row    *apiv1.Price
	rate   decimal.Decimal
	baseID string
	// valueBase is the price in its own base, before conversion; change is
	// computed there so a moving cross rate does not read as a moving asset.
	valueBase decimal.Decimal
}

// assetPricing resolves (and caches in `cache`) price, change and label for assetID.
//
// Price resolution mirrors portfolio.Handler.unitPrice, deliberately down to the
// wording: among the ways to express the asset, the FRESHEST candidate wins, and
// the market-depth gate reads every candidate that reports a volume rather than
// only the chosen one. See unitPrice for why each of those is the way it is.
//
// Duplicated consciously — second consumer; extract a shared pricing package on
// the third (rule of three). Until then TestTotalAndHeatmapAgree is what keeps
// the two copies answering the same question: a total and a map that disagree
// are the same failure as способ #6, moved between two backend services.
func (h *Handler) assetPricing(ctx context.Context, cache map[string]*assetPricing, assetID, quoteAssetID string, from time.Time) (*assetPricing, error) {
	if ap, ok := cache[assetID]; ok {
		return ap, nil
	}
	ap := &assetPricing{label: assetID, reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE}
	cache[assetID] = ap

	if err := h.resolveLabel(ctx, ap, assetID); err != nil {
		return nil, err
	}

	candidates, err := h.quotes(ctx, assetID, quoteAssetID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return ap, nil
	}
	for _, c := range candidates {
		if h.markThin(ctx, ap, assetID, c.row, c.rate) {
			return ap, nil
		}
	}

	best := freshestQuote(candidates)
	ap.unit, ap.priced, ap.quotedAt = best.unit, true, pricefresh.QuotedAt(best.row)

	// Change is read in the pair the price came from: the conversion rate
	// cancels out only approximately, and it is the asset that is being asked
	// about, not the currency it is converted through.
	then, ok, err := h.histPrice(ctx, assetID, best.baseID, from)
	if err != nil {
		return nil, err
	}
	if ok {
		ap.changePct = changePct(best.valueBase, then)
	}
	return ap, nil
}

// quotes collects every way this asset can be expressed in quoteAssetID: its own
// freshest print converted from whatever pair it trades in, and a price quoted
// straight in the quote asset. They are the same row whenever the freshest print
// is already in the quote asset, and then there is nothing to choose between.
func (h *Handler) quotes(ctx context.Context, assetID, quoteAssetID string) ([]quote, error) {
	one := decimal.NewFromInt(1)
	var out []quote

	baseID, value, row, ok, err := h.latestAnyBase(ctx, assetID)
	if err != nil {
		return nil, err
	}
	direct := ok && baseID == quoteAssetID
	switch {
	case direct:
		out = append(out, quote{unit: value, row: row, rate: one, baseID: baseID, valueBase: value})
	case ok:
		rate, hasRate, err := h.crossRate(ctx, baseID, quoteAssetID)
		if err != nil {
			return nil, err
		}
		if hasRate {
			out = append(out, quote{unit: value.Mul(rate), row: row, rate: rate, baseID: baseID, valueBase: value})
		}
	}

	if !direct {
		unit, row, ok, err := h.realPrice(ctx, assetID, quoteAssetID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, quote{unit: unit, row: row, rate: one, baseID: quoteAssetID, valueBase: unit})
		}
	}
	return out, nil
}

// freshestQuote picks the most recently observed candidate. A quote with no
// timestamp sorts oldest: it cannot claim currency it never stated.
func freshestQuote(candidates []quote) quote {
	best := candidates[0]
	for _, c := range candidates[1:] {
		if pricefresh.QuotedAt(c.row).After(pricefresh.QuotedAt(best.row)) {
			best = c
		}
	}
	return best
}

// markThin applies the market-depth gate to the asset's own price row and reports
// whether it fired. A thin quote leaves the asset unpriced, so it drops off the map
// rather than drawing a node out of a market that cannot absorb the position — the
// MNEP case that opened personal-6ae.
//
// It records which of the two absences this is: a quote that exists but has no
// market behind it is a decision about the asset, while no quote at all is a
// coverage gap to close upstream. The response's coverage block carries the
// distinction to the caller.
func (h *Handler) markThin(ctx context.Context, ap *assetPricing, assetID string, price *apiv1.Price, rate decimal.Decimal) bool {
	if !marketdepth.Thin(price, rate) {
		return false
	}
	ap.reason = apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET
	h.log.DebugContext(ctx, "heatmap: skipping asset with no market behind its quote",
		"asset_id", assetID, "label", ap.label)
	return true
}

func (h *Handler) resolveLabel(ctx context.Context, ap *assetPricing, assetID string) error {
	resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: assetID}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil // keep the ID as label
		}
		return err
	}
	switch {
	case resp.Msg.Symbol != nil && *resp.Msg.Symbol != "":
		ap.label = *resp.Msg.Symbol
	case resp.Msg.Name != "":
		ap.label = resp.Msg.Name
	}
	return nil
}

func changePct(now, then decimal.Decimal) float64 {
	if then.IsZero() {
		return 0
	}
	pct, _ := now.Sub(then).Div(then).Mul(decimal.NewFromInt(100)).Float64()
	return pct
}

// histPrice returns the first stored price of assetID in baseID at or after
// `from` as a real-unit decimal. ok is false when there is no history.
func (h *Handler) histPrice(ctx context.Context, assetID, baseID string, from time.Time) (decimal.Decimal, bool, error) {
	pageSize := int32(1)
	resp, err := h.mdClient.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
		AssetId:     assetID,
		BaseAssetId: baseID,
		From:        timestamppb.New(from),
		PageSize:    &pageSize,
	}))
	if err != nil {
		return decimal.Zero, false, err
	}
	if len(resp.Msg.Prices) == 0 {
		return decimal.Zero, false, nil
	}
	return realUnit(resp.Msg.Prices[0], h, assetID)
}

// realPrice returns the latest price of assetID in baseID as a real-unit decimal
// (value = last / 10^decimals), together with the price row it came from — callers
// need the market context on it, not just the number. ok is false when no price
// exists (NotFound) or the stored value is unparseable; a non-nil error is returned
// only for unexpected failures.
func (h *Handler) realPrice(ctx context.Context, assetID, baseID string) (decimal.Decimal, *apiv1.Price, bool, error) {
	resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, BaseAssetId: baseID,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return decimal.Zero, nil, false, nil
		}
		return decimal.Zero, nil, false, err
	}
	value, ok, err := realUnit(resp.Msg, h, assetID)
	return value, resp.Msg, ok, err
}

// latestAnyBase returns the asset's most recent price regardless of base, as the base
// asset ID, the real-unit value and the price row. ok is false when no price exists.
func (h *Handler) latestAnyBase(ctx context.Context, assetID string) (string, decimal.Decimal, *apiv1.Price, bool, error) {
	resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, // BaseAssetId omitted → latest in any base
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return "", decimal.Zero, nil, false, nil
		}
		return "", decimal.Zero, nil, false, err
	}
	value, ok, err := realUnit(resp.Msg, h, assetID)
	if err != nil || !ok {
		return "", decimal.Zero, nil, false, err
	}
	return resp.Msg.BaseAssetId, value, resp.Msg, true, nil
}

// crossRate returns how many units of quoteID one unit of baseID is worth, using a
// direct baseID/quoteID price or, failing that, the inverse quoteID/baseID price.
//
// The price rows are discarded on purpose: a currency pair is not subject to the
// market-depth gate, which applies to the asset's own quote (see markThin).
func (h *Handler) crossRate(ctx context.Context, baseID, quoteID string) (decimal.Decimal, bool, error) {
	if r, _, ok, err := h.realPrice(ctx, baseID, quoteID); err != nil || ok {
		return r, ok, err
	}
	inv, _, ok, err := h.realPrice(ctx, quoteID, baseID)
	if err != nil || !ok || inv.IsZero() {
		return decimal.Zero, false, err
	}
	return decimal.NewFromInt(1).Div(inv), true, nil
}

// realUnit converts a Price proto into a real-unit decimal (last / 10^decimals).
// ok is false when the stored value is unparseable (logged, not fatal).
func realUnit(p *apiv1.Price, h *Handler, assetID string) (decimal.Decimal, bool, error) {
	last, err := decimal.NewFromString(p.Last)
	if err != nil {
		h.log.Warn("skip price with unparseable last",
			"asset_id", assetID, "base_asset_id", p.BaseAssetId, "last", p.Last, "error", err)
		return decimal.Zero, false, nil
	}
	return last.Shift(-decI32(p.Decimals)), true, nil
}

func decI32(d uint32) int32 {
	if d > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(d)
}

// markNeverPriced upgrades NO_QUOTE to NEVER_PRICED for assets whose sources
// have all been asked and none has ever answered, and dates the silence.
//
// The heatmap does this because it embeds the same ValuationCoverage as the
// portfolio total: the two surfaces disagreeing about why one holding has no
// price is the divergence that message exists to prevent.
//
// Best-effort, like its counterpart in portfolio: without the attempt log the
// reason stays what it already was.
func (h *Handler) markNeverPriced(ctx context.Context, unpriced []*apiv1.UnpricedHolding) {
	ids := make([]string, 0, len(unpriced))
	seen := make(map[string]struct{}, len(unpriced))
	for _, u := range unpriced {
		if u.GetReason() != apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE {
			continue
		}
		if _, dup := seen[u.GetAssetId()]; dup {
			continue
		}
		seen[u.GetAssetId()] = struct{}{}
		ids = append(ids, u.GetAssetId())
	}
	if len(ids) == 0 {
		return
	}

	resp, err := h.mdClient.GetPricingStatus(ctx, connect.NewRequest(&apiv1.GetPricingStatusRequest{AssetIds: ids}))
	if err != nil {
		h.log.WarnContext(ctx, "heatmap: pricing status unavailable, reporting unpriced holdings without it",
			"asset_count", len(ids), "error", err)
		return
	}

	askedSince := make(map[string]*timestamppb.Timestamp, len(resp.Msg.GetStatuses()))
	for _, st := range resp.Msg.GetStatuses() {
		if st.GetEverPriced() || st.GetFirstAskedAt() == nil {
			continue
		}
		askedSince[st.GetAssetId()] = st.GetFirstAskedAt()
	}
	for _, u := range unpriced {
		if u.GetReason() != apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE {
			continue
		}
		if since, ok := askedSince[u.GetAssetId()]; ok {
			u.Reason = apiv1.UnpricedReason_UNPRICED_REASON_NEVER_PRICED
			u.AskedSince = since
		}
	}
}
