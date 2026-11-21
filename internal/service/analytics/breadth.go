package analytics

import (
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// breadthTally builds HeatmapBreadth from the holdings and leaves the heatmap
// draws, so the strip cannot drift from the tiles.
type breadthTally struct {
	up, noHistory       uint32
	weightedSum, weight decimal.Decimal // change weighted by holding value
	best, worst         *extremeCandidate
}

// extremeCandidate is a drawn leaf considered for best or worst.
type extremeCandidate struct {
	node      *apiv1.HeatmapNode
	changePct float64
	stale     bool
}

// addHolding counts one priced holding of value at the asset's pricing.
func (b *breadthTally) addHolding(ap *assetPricing, value decimal.Decimal) {
	if !ap.hasHistory {
		b.noHistory++
		return
	}
	if ap.changePct > 0 {
		b.up++
	}
	b.weightedSum = b.weightedSum.Add(value.Mul(decimal.NewFromFloat(ap.changePct)))
	b.weight = b.weight.Add(value)
}

// addLeaf considers one drawn leaf for best and worst. On equal change the
// larger leaf wins, then the smaller id: leaves come out of a map, so the
// order they arrive in must not pick the extreme.
func (b *breadthTally) addLeaf(node *apiv1.HeatmapNode, ap *assetPricing) {
	if !ap.hasHistory {
		return
	}
	c := &extremeCandidate{node: node, changePct: ap.changePct, stale: ap.stale}
	if b.best == nil || c.beats(b.best, 1) {
		b.best = c
	}
	if b.worst == nil || c.beats(b.worst, -1) {
		b.worst = c
	}
}

// beats reports whether c displaces held as the extreme in direction dir:
// 1 for the highest change, -1 for the lowest.
func (c *extremeCandidate) beats(held *extremeCandidate, dir float64) bool {
	if c.changePct != held.changePct {
		return dir*c.changePct > dir*held.changePct
	}
	if c.node.Size != held.node.Size {
		return c.node.Size > held.node.Size
	}
	return c.node.Id < held.node.Id
}

func (c *extremeCandidate) leaf() *apiv1.HeatmapBreadthLeaf {
	if c == nil {
		return nil
	}
	return &apiv1.HeatmapBreadthLeaf{
		NodeId: c.node.Id, AssetId: c.node.AssetId, Label: c.node.Label,
		ChangePct: c.changePct, Stale: c.stale,
	}
}

// result takes the priced, unpriced and stale counts from coverage: they are
// the same holdings, so they are counted once.
func (b *breadthTally) result(coverage *apiv1.ValuationCoverage) *apiv1.HeatmapBreadth {
	out := &apiv1.HeatmapBreadth{
		UpCount:        b.up,
		PricedCount:    coverage.PricedCount,
		UnpricedCount:  coverage.UnpricedCount,
		NoHistoryCount: b.noHistory,
		StaleCount:     coverage.StaleCount,
		Best:           b.best.leaf(),
		Worst:          b.worst.leaf(),
	}
	if !b.weight.IsZero() {
		avg := b.weightedSum.Div(b.weight).InexactFloat64()
		out.WeightedChangePct = &avg
	}
	return out
}
