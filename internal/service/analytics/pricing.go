package analytics

import (
	"context"
	"math"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/foxcool/greedy-eye/internal/quoting"
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
// Price resolution lives in internal/quoting, shared with the portfolio total:
// among the ways to express the asset the FRESHEST candidate wins, and the
// market-depth gate reads every candidate that reports a volume rather than only
// the chosen one. See that package for why each is the way it is.
//
// The two surfaces sharing one implementation is the point. They used to carry a
// copy each, and TestTotalAndHeatmapAgree existed to make a divergence loud: a
// total and a map that disagree are способ #6 moved between two backend
// services. The test still guards the agreement; the extraction removes the way
// to break it.
func (h *Handler) assetPricing(ctx context.Context, cache map[string]*assetPricing, assetID, quoteAssetID string, from time.Time) (*assetPricing, error) {
	if ap, ok := cache[assetID]; ok {
		return ap, nil
	}
	ap := &assetPricing{label: assetID, reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE}
	cache[assetID] = ap

	if err := h.resolveLabel(ctx, ap, assetID); err != nil {
		return nil, err
	}

	candidates, err := quoting.Candidates(ctx, h.mdClient, h.log, assetID, quoteAssetID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return ap, nil
	}
	if thin, isThin := quoting.AnyThin(candidates); isThin {
		h.markThin(ctx, ap, assetID, thin.Row)
		return ap, nil
	}

	best := quoting.Freshest(candidates)
	ap.unit, ap.priced, ap.quotedAt = best.Unit, true, pricefresh.QuotedAt(best.Row)

	// Change is read in the pair the price came from: the conversion rate
	// cancels out only approximately, and it is the asset that is being asked
	// about, not the currency it is converted through.
	then, ok, err := h.histPrice(ctx, assetID, best.BaseID, from)
	if err != nil {
		return nil, err
	}
	if ok {
		ap.changePct = changePct(best.ValueBase, then)
	}
	return ap, nil
}

// markThin records that the market-depth gate fired on this asset. A thin quote
// leaves the asset unpriced, so it drops off the map rather than drawing a node
// out of a market that cannot absorb the position — the MNEP case that opened
// personal-6ae. The gate itself is quoting.AnyThin; this is the heatmap's half:
// which of the two absences to report.
//
// A quote that exists but has no market behind it is a decision about the asset,
// while no quote at all is a coverage gap to close upstream. The response's
// coverage block carries the distinction to the caller.
func (h *Handler) markThin(ctx context.Context, ap *assetPricing, assetID string, price *apiv1.Price) {
	ap.reason = apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET
	h.log.DebugContext(ctx, "heatmap: skipping asset with no market behind its quote",
		"asset_id", assetID, "label", ap.label, "price_base", price.GetBaseAssetId())
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
	return quoting.RealUnit(resp.Msg.Prices[0], h.log, assetID)
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
