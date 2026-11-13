package entity

import (
	"time"

	"github.com/shopspring/decimal"
)

// MacroObservation is one published print of a macro series, such as a policy
// rate.
//
// Date is the day the publisher states the value for, not the moment anything
// was fetched: a rate unchanged for weeks is still dated by its latest print,
// and stamping it "now" would claim a publication that never happened. It is a
// calendar day carried at midnight in the publisher's own zone.
type MacroObservation struct {
	Value decimal.Decimal
	Date  time.Time
}
