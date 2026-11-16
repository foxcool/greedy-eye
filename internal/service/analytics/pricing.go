package analytics

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/foxcool/greedy-eye/internal/quoting"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
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

// priceAssets resolves price, change and label for every asset in assetIDs,
// reading market data once per set rather than once per asset: in a split
// deployment each call is a network hop, and a map over hundreds of holdings
// must not cost hundreds of them. The reads, in order, each depending on the
// one before:
//
//  1. labels for every asset (ListAssets by id);
//  2. each asset's latest price in any base, and in the quote asset;
//  3. the conversion rate, both directions, for every base step 2 found;
//  4. each priced asset's first price of the window, in the pair chosen.
//
// Price resolution lives in internal/quoting, shared with the portfolio total:
// among the ways to express the asset the FRESHEST candidate wins, and the
// market-depth gate reads every candidate that reports a volume rather than only
// the chosen one. See that package for why each is the way it is. It runs here
// unchanged, against the prices collected in steps 2-3 (quoting.Prefetch), so
// the batch cannot price an asset differently from the per-pair reads it
// replaces.
//
// Sharing one implementation is the point: a total and a map that disagree are
// one number with two authors. TestTotalAndHeatmapAgree still guards the
// agreement; the extraction removes the way to break it.
func (h *Handler) priceAssets(ctx context.Context, assetIDs []string, quoteAssetID string, from time.Time) (map[string]*assetPricing, error) {
	out := make(map[string]*assetPricing, len(assetIDs))
	for _, id := range assetIDs {
		out[id] = &assetPricing{label: id, reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE}
	}
	if len(assetIDs) == 0 {
		return out, nil
	}
	if err := h.resolveLabels(ctx, out, assetIDs); err != nil {
		return nil, err
	}

	// Steps 2 and 3: exactly the pairs quoting.Candidates asks about.
	prices, err := quoting.Prefetch(ctx, h.mdClient, assetIDs, quoteAssetID)
	if err != nil {
		return nil, err
	}

	best := map[string]quoting.Quote{}
	for _, id := range assetIDs {
		ap := out[id]
		candidates, missing, err := quoting.Candidates(ctx, prices, h.log, id, quoteAssetID)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			ap.reason = missing.Reason()
			continue
		}
		if thin, isThin := quoting.AnyThin(candidates); isThin {
			h.markThin(ctx, ap, id, thin.Row)
			continue
		}
		b := quoting.Freshest(candidates)
		ap.unit, ap.priced, ap.quotedAt = b.Unit, true, pricefresh.QuotedAt(b.Row)
		best[id] = b
	}

	// Step 4. Change is read in the pair the price came from: the conversion
	// rate cancels out only approximately, and it is the asset that is being
	// asked about, not the currency it is converted through.
	var lookups []*apiv1.PriceLookup
	for _, id := range assetIDs {
		if b, ok := best[id]; ok {
			lookups = append(lookups, &apiv1.PriceLookup{AssetId: id, BaseAssetId: b.BaseID, From: timestamppb.New(from)})
		}
	}
	history := quoting.Prefetched{}
	if err := history.Fetch(ctx, h.mdClient, lookups); err != nil {
		return nil, err
	}
	for id, b := range best {
		row := history[quoting.PriceKey{Asset: id, Base: b.BaseID, Windowed: true}]
		if row == nil {
			continue // no history: change stays 0
		}
		then, ok, err := quoting.RealUnit(row, h.log, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id].changePct = changePct(b.ValueBase, then)
		}
	}
	return out, nil
}

// maxBatch is the most ids one ListAssets request carries, the bound market
// data enforces. A set larger than that costs one more call per batch, not one
// per asset.
const maxBatch = 1000

// resolveLabels names each asset by symbol, falling back to name; an asset
// the catalogue does not return keeps its ID as label.
func (h *Handler) resolveLabels(ctx context.Context, out map[string]*assetPricing, assetIDs []string) error {
	for chunk := range slices.Chunk(assetIDs, maxBatch) {
		req := &apiv1.ListAssetsRequest{Ids: chunk, PageSize: proto.Int32(int32(len(chunk)))} // #nosec G115 -- bounded by maxBatch
		for {
			resp, err := h.mdClient.ListAssets(ctx, connect.NewRequest(req))
			if err != nil {
				return err
			}
			for _, a := range resp.Msg.GetAssets() {
				ap, ok := out[a.GetId()]
				if !ok {
					continue
				}
				switch {
				case a.GetSymbol() != "":
					ap.label = a.GetSymbol()
				case a.GetName() != "":
					ap.label = a.GetName()
				}
			}
			next := resp.Msg.GetNextPageToken()
			if next == "" || len(resp.Msg.GetAssets()) == 0 {
				break
			}
			req.PageToken = &next
		}
	}
	return nil
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

func changePct(now, then decimal.Decimal) float64 {
	if then.IsZero() {
		return 0
	}
	pct, _ := now.Sub(then).Div(then).Mul(decimal.NewFromInt(100)).Float64()
	return pct
}

// sharpenUnpriced replaces NO_QUOTE with the more specific reason the pricing
// status supports, through the same quoting.SharpenNoQuote the portfolio total
// uses: the heatmap embeds the same ValuationCoverage, and the two surfaces
// disagreeing about why one holding has no price is the divergence that
// message exists to prevent.
//
// Best-effort, like its counterpart in portfolio: without the status the
// reason stays what it already was.
func (h *Handler) sharpenUnpriced(ctx context.Context, unpriced []*apiv1.UnpricedHolding) {
	ids := quoting.NoQuoteAssetIDs(unpriced)
	if len(ids) == 0 {
		return
	}
	resp, err := h.mdClient.GetPricingStatus(ctx, connect.NewRequest(&apiv1.GetPricingStatusRequest{AssetIds: ids}))
	if err != nil {
		h.log.WarnContext(ctx, "heatmap: pricing status unavailable, reporting unpriced holdings without it",
			"asset_count", len(ids), "error", err)
		return
	}
	quoting.SharpenNoQuote(unpriced, resp.Msg.GetStatuses())
}
