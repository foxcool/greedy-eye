package quoting

import (
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// Outcome says whether a holding could be valued, and if not, why. The proto
// enum is the wire shape; this is the domain one, so the pricing code never has
// to spell UNPRICED_REASON_UNSPECIFIED to mean success.
type Outcome int

const (
	Priced Outcome = iota
	NoQuote
	ThinMarket
)

// Reason maps a failed outcome onto the wire enum. Meaningless when Priced;
// NO_QUOTE is the fallback because "no price row anywhere" is the outcome of
// every path that simply fails to find one.
func (o Outcome) Reason() apiv1.UnpricedReason {
	if o == ThinMarket {
		return apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET
	}
	return apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE
}
