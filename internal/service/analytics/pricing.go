package analytics

import (
	"context"
	"math"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// assetPricing is everything the heatmap needs to know about one asset.
type assetPricing struct {
	unit      decimal.Decimal // current per-token price in the quote asset
	priced    bool            // false when no price path exists → asset is skipped
	changePct float64         // signed % change over the window; 0 when history is missing
	label     string          // asset symbol, falling back to name, then ID
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
	ap := &assetPricing{label: assetID}
	cache[assetID] = ap

	if err := h.resolveLabel(ctx, ap, assetID); err != nil {
		return nil, err
	}

	// Direct price in the quote asset: value and change come from the same pair.
	if unit, ok, err := h.realPrice(ctx, assetID, quoteAssetID); err != nil {
		return nil, err
	} else if ok {
		ap.unit, ap.priced = unit, true
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
	baseID, nowBase, ok, err := h.latestAnyBase(ctx, assetID)
	if err != nil || !ok {
		return ap, err
	}
	rate, ok, err := h.crossRate(ctx, baseID, quoteAssetID)
	if err != nil || !ok {
		return ap, err
	}
	ap.unit, ap.priced = nowBase.Mul(rate), true
	thenBase, ok, err := h.histPrice(ctx, assetID, baseID, from)
	if err != nil {
		return nil, err
	}
	if ok {
		ap.changePct = changePct(nowBase, thenBase)
	}
	return ap, nil
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
// (value = last / 10^decimals). ok is false when no price exists (NotFound) or the
// stored value is unparseable; a non-nil error is returned only for unexpected failures.
func (h *Handler) realPrice(ctx context.Context, assetID, baseID string) (decimal.Decimal, bool, error) {
	resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, BaseAssetId: baseID,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return decimal.Zero, false, nil
		}
		return decimal.Zero, false, err
	}
	return realUnit(resp.Msg, h, assetID)
}

// latestAnyBase returns the asset's most recent price regardless of base, as the base
// asset ID and the real-unit value. ok is false when no price exists.
func (h *Handler) latestAnyBase(ctx context.Context, assetID string) (string, decimal.Decimal, bool, error) {
	resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, // BaseAssetId omitted → latest in any base
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return "", decimal.Zero, false, nil
		}
		return "", decimal.Zero, false, err
	}
	value, ok, err := realUnit(resp.Msg, h, assetID)
	if err != nil || !ok {
		return "", decimal.Zero, false, err
	}
	return resp.Msg.BaseAssetId, value, true, nil
}

// crossRate returns how many units of quoteID one unit of baseID is worth, using a
// direct baseID/quoteID price or, failing that, the inverse quoteID/baseID price.
func (h *Handler) crossRate(ctx context.Context, baseID, quoteID string) (decimal.Decimal, bool, error) {
	if r, ok, err := h.realPrice(ctx, baseID, quoteID); err != nil || ok {
		return r, ok, err
	}
	inv, ok, err := h.realPrice(ctx, quoteID, baseID)
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
