package execution

import (
	"context"
	"fmt"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// Account is the account a trade is placed on; its type selects the venue.
type Account struct {
	ID   string
	Type apiv1.AccountType
}

// Key identifies one trade of one execution. Its string form is the
// idempotency key a venue receives and the journal's external_id.
type Key struct {
	ExecutionID string
	Index       int
}

func (k Key) String() string { return fmt.Sprintf("%s:%d", k.ExecutionID, k.Index) }

// OutcomeStatus is a venue's answer to a trade. Only terminal answers exist
// so far; the zero value stands for anything else and the core refuses it
// (see Apply).
type OutcomeStatus int

const (
	OutcomeFilled OutcomeStatus = iota + 1
	OutcomeRejected
)

// Fill is what a filled trade did, in asset units.
type Fill struct {
	BaseAmount  decimal.Decimal
	QuoteAmount decimal.Decimal
	Price       decimal.Decimal
	PriceAsOf   time.Time
}

// Outcome is a venue's answer to one Submit.
type Outcome struct {
	Status OutcomeStatus
	Fill   Fill
	Reason string
	// HoldingIDs are the holdings the venue changed.
	HoldingIDs []string
}

// Venue places trades. An error means the venue could not answer; the trade
// is then rejected with it.
type Venue interface {
	Submit(ctx context.Context, account Account, trade Trade, key Key) (Outcome, error)
}

// Venues is the registry of venues by account type.
type Venues map[apiv1.AccountType]Venue

func (r Venues) Lookup(t apiv1.AccountType) (Venue, bool) {
	v, ok := r[t]
	return v, ok
}
