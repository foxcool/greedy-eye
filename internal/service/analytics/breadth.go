package analytics

import (
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// breadth tallies HeatmapBreadth while the heatmap values its holdings, so the
// strip reads the very prices, changes and staleness the tiles are drawn from
// and cannot drift from the map. Counts are per holding, matching
// ValuationCoverage; the extremes are per leaf, matching the nodes.
type breadth struct {
	msg apiv1.HeatmapBreadth
	// Value-weighted change over the holdings that have history.
	weightedSum, weight decimal.Decimal
	// Sizes of the current extremes, for the tie-break in addLeaf.
	bestSize, worstSize float64
}

// addHolding counts one priced holding of value at the asset's pricing.
func (b *breadth) addHolding(ap *assetPricing, value decimal.Decimal, stale bool) {
	b.msg.PricedCount++
	if stale {
		b.msg.StaleCount++
	}
	if !ap.hasHistory {
		b.msg.NoHistoryCount++
		return
	}
	if ap.changePct > 0 {
		b.msg.UpCount++
	}
	b.weightedSum = b.weightedSum.Add(value.Mul(decimal.NewFromFloat(ap.changePct)))
	b.weight = b.weight.Add(value)
}

// addLeaf considers one drawn leaf for best and worst. On equal change the
// larger leaf wins, then the smaller id: leaves come out of a map, so the
// order they arrive in must not pick the extreme.
func (b *breadth) addLeaf(node *apiv1.HeatmapNode, ap *assetPricing, stale bool) {
	if !ap.hasHistory {
		return
	}
	leaf := func() *apiv1.HeatmapBreadthLeaf {
		return &apiv1.HeatmapBreadthLeaf{
			NodeId: node.Id, AssetId: node.AssetId, Label: node.Label,
			ChangePct: ap.changePct, Stale: stale,
		}
	}
	if b.msg.Best == nil || ap.changePct > b.msg.Best.ChangePct ||
		(ap.changePct == b.msg.Best.ChangePct && winsTie(node, b.bestSize, b.msg.Best.NodeId)) {
		b.msg.Best, b.bestSize = leaf(), node.Size
	}
	if b.msg.Worst == nil || ap.changePct < b.msg.Worst.ChangePct ||
		(ap.changePct == b.msg.Worst.ChangePct && winsTie(node, b.worstSize, b.msg.Worst.NodeId)) {
		b.msg.Worst, b.worstSize = leaf(), node.Size
	}
}

// winsTie reports whether node displaces the holder of an extreme it ties on change.
func winsTie(node *apiv1.HeatmapNode, heldSize float64, heldID string) bool {
	if node.Size != heldSize {
		return node.Size > heldSize
	}
	return node.Id < heldID
}

func (b *breadth) result(unpriced uint32) *apiv1.HeatmapBreadth {
	out := &b.msg
	out.UnpricedCount = unpriced
	if !b.weight.IsZero() {
		avg := b.weightedSum.Div(b.weight).InexactFloat64()
		out.WeightedChangePct = &avg
	}
	return out
}
