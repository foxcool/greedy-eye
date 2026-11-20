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

// addLeaf considers one drawn leaf for best and worst. On a tie the leaf seen
// first keeps the place; callers pass leaves largest first, so the bigger
// position names the extreme.
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
	if b.msg.Best == nil || ap.changePct > b.msg.Best.ChangePct {
		b.msg.Best = leaf()
	}
	if b.msg.Worst == nil || ap.changePct < b.msg.Worst.ChangePct {
		b.msg.Worst = leaf()
	}
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
