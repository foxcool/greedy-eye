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
	// NoCrossRate is the asset being quoted in a base with no rate to the
	// requested currency, in either direction.
	//
	// It is kept apart from NoQuote because the two ask for opposite work, and
	// because collapsing them hid a live failure for months: a USDT twin split
	// the identity so Binance quoted into one row while the USDT/USD rate was
	// written against the other, and 74 holdings left the total reading exactly
	// like assets nobody had ever priced.
	NoCrossRate
)

// Reason maps a failed outcome onto the wire enum. Meaningless when Priced;
// NO_QUOTE is the fallback because "no price row anywhere" is the outcome of
// every path that simply fails to find one.
func (o Outcome) Reason() apiv1.UnpricedReason {
	switch o {
	case ThinMarket:
		return apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET
	case NoCrossRate:
		return apiv1.UnpricedReason_UNPRICED_REASON_NO_CROSS_RATE
	default:
		return apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE
	}
}
