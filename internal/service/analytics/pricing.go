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

// assetPricing resolves (and caches in `cache`) price, change and label for assetID.
//
// Price resolution mirrors portfolio.Handler.unitPrice: direct quote first, then
// cross through the asset's actual traded base. Duplicated consciously — second
// consumer; extract a shared pricing package on the third (rule of three).
func (h *Handler) assetPricing(ctx context.Context, cache map[string]*assetPricing, assetID, quoteAssetID string, from time.Time) (*assetPricing, error) {
	if ap, ok := cache[assetID]; ok {
		return ap, nil
	}
	ap := &assetPricing{label: assetID, reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE}
	cache[assetID] = ap

	if err := h.resolveLabel(ctx, ap, assetID); err != nil {
		return nil, err
	}

	// Direct price in the quote asset: value and change come from the same pair.
	if unit, price, ok, err := h.realPrice(ctx, assetID, quoteAssetID); err != nil {
		return nil, err
	} else if ok {
		if h.markThin(ctx, ap, assetID, price, decimal.NewFromInt(1)) {
			return ap, nil
		}
		ap.unit, ap.priced, ap.quotedAt = unit, true, pricefresh.QuotedAt(price)
		then, ok, err := h.histPrice(ctx, assetID, quoteAssetID, from)
		if err != nil {
			return nil, err
		}
		if ok {
			ap.changePct = changePct(unit, then)
		}
		return ap, nil
	}

	// Cross price: latest in the asset's own traded base, converted to quote.
	// Change is computed in base terms (the conversion rate cancels out only
	// approximately, but it is the pair the asset actually trades in).
	baseID, nowBase, price, ok, err := h.latestAnyBase(ctx, assetID)
	if err != nil || !ok {
		return ap, err
	}
	rate, ok, err := h.crossRate(ctx, baseID, quoteAssetID)
	if err != nil || !ok {
		return ap, err
	}
	// Volume is denominated in the traded base, so it converts with the same rate.
	if h.markThin(ctx, ap, assetID, price, rate) {
		return ap, nil
	}
	ap.unit, ap.priced, ap.quotedAt = nowBase.Mul(rate), true, pricefresh.QuotedAt(price)
	thenBase, ok, err := h.histPrice(ctx, assetID, baseID, from)
	if err != nil {
		return nil, err
	}
	if ok {
		ap.changePct = changePct(nowBase, thenBase)
	}
	return ap, nil
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
