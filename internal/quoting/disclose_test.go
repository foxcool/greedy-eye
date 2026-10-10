package quoting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

func TestSharpenNoQuote(t *testing.T) {
	asked := timestamppb.New(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	unpriced := []*apiv1.UnpricedHolding{
		{HoldingId: "h1", AssetId: "contested-and-silent", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
		{HoldingId: "h2", AssetId: "silent", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
		{HoldingId: "h3", AssetId: "contested-thin", Reason: apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET},
		{HoldingId: "h4", AssetId: "unknown", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
		{HoldingId: "h5", AssetId: "priced-once", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
	}
	statuses := []*apiv1.AssetPricingStatus{
		// Asked before the twin appeared, and never answered: the contest is
		// the cause, the silence its symptom.
		{AssetId: "contested-and-silent", AmbiguousTicker: true, FirstAskedAt: asked},
		{AssetId: "silent", FirstAskedAt: asked},
		{AssetId: "contested-thin", AmbiguousTicker: true},
		{AssetId: "priced-once", EverPriced: true, FirstAskedAt: asked},
	}

	SharpenNoQuote(unpriced, statuses)

	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_AMBIGUOUS_TICKER, unpriced[0].Reason, "ambiguous outranks never-priced")
	assert.Nil(t, unpriced[0].AskedSince, "asked_since belongs to NEVER_PRICED only")
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NEVER_PRICED, unpriced[1].Reason)
	assert.Equal(t, asked.AsTime(), unpriced[1].AskedSince.AsTime())
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET, unpriced[2].Reason, "a quote that exists outranks both")
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE, unpriced[3].Reason, "no status, no sharpening")
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE, unpriced[4].Reason, "a source once answered")
}

func TestNoQuoteAssetIDs(t *testing.T) {
	got := NoQuoteAssetIDs([]*apiv1.UnpricedHolding{
		{AssetId: "a", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
		{AssetId: "a", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
		{AssetId: "b", Reason: apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET},
		{AssetId: "c", Reason: apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE},
	})
	assert.Equal(t, []string{"a", "c"}, got)
}
