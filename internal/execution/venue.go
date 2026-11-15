package execution

import (
	"context"
	"fmt"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// Account is the account a trade is placed on; its type selects the venue.
// SlippageBps is the account policy's limit, for a venue that quotes before it
// trades (the paper venue fills at the plan's price and ignores it).
type Account struct {
	ID          string
	Type        apiv1.AccountType
	SlippageBps uint32
}

// Key identifies one trade of one execution. Its string form is the
// idempotency key a venue receives and the journal's external_id.
type Key struct {
	ExecutionID string
	Index       int
}

func (k Key) String() string { return fmt.Sprintf("%s:%d", k.ExecutionID, k.Index) }

// OutcomeStatus is a venue's answer to a trade. Filled and rejected are
// final; the others say the trade is waiting on someone else — a signature,
// then a block — and a WaitingVenue is asked again later.
type OutcomeStatus int

const (
	OutcomeFilled OutcomeStatus = iota + 1
	OutcomeRejected
	OutcomePendingSignature
	OutcomeSubmitted
	OutcomeFailed
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
	// VenueRef is how the venue finds the trade again (a Safe's safeTxHash);
	// TxHash is the chain transaction once there is one.
	VenueRef string
	TxHash   string
}

// Venue places trades. An error means the venue could not answer; the trade
// is then rejected with it.
type Venue interface {
	Submit(ctx context.Context, account Account, trade Trade, key Key) (Outcome, error)
}

// WaitingVenue is a venue whose trades can wait after Submit. Check answers
// where a waiting trade is now; answering the state it is already in leaves
// it as it is.
type WaitingVenue interface {
	Venue
	Check(ctx context.Context, account Account, key Key, r TradeResult) (Outcome, error)
}

// Venues is the registry of venues by account type.
type Venues map[apiv1.AccountType]Venue

func (r Venues) Lookup(t apiv1.AccountType) (Venue, bool) {
	v, ok := r[t]
	return v, ok
}
