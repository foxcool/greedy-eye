package quoting

import (
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// NoQuoteAssetIDs lists, once each, the assets whose unpriced holdings still
// carry the generic NO_QUOTE: the ones the pricing status can say more about.
func NoQuoteAssetIDs(unpriced []*apiv1.UnpricedHolding) []string {
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
	return ids
}

// SharpenNoQuote replaces NO_QUOTE with the more specific reason the pricing
// status supports: AMBIGUOUS_TICKER when no source is asked because the ticker
// is contested, else NEVER_PRICED, dated, when every source was asked and none
// ever answered.
//
// One function for every surface that embeds ValuationCoverage. The total and
// the heatmap disagreeing about why one holding has no price is the divergence
// that message exists to prevent, and a rule written twice is how they would.
//
// Only NO_QUOTE is sharpened: THIN_MARKET and NO_CROSS_RATE mean a quote
// exists, which is a stronger statement than anything the attempt log or the
// catalogue can add.
func SharpenNoQuote(unpriced []*apiv1.UnpricedHolding, statuses []*apiv1.AssetPricingStatus) {
	byID := make(map[string]*apiv1.AssetPricingStatus, len(statuses))
	for _, st := range statuses {
		byID[st.GetAssetId()] = st
	}
	for _, u := range unpriced {
		if u.GetReason() != apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE {
			continue
		}
		st, ok := byID[u.GetAssetId()]
		if !ok {
			continue
		}
		switch {
		case st.GetAmbiguousTicker():
			u.Reason = apiv1.UnpricedReason_UNPRICED_REASON_AMBIGUOUS_TICKER
		case !st.GetEverPriced() && st.GetFirstAskedAt() != nil:
			u.Reason = apiv1.UnpricedReason_UNPRICED_REASON_NEVER_PRICED
			u.AskedSince = st.GetFirstAskedAt()
		}
	}
}
