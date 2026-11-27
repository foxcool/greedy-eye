package entity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// BrokerIncome is one payment of income into a broker account, or of a tax
// on it, as the broker booked it.
type BrokerIncome struct {
	// ID is the broker's id of the operation.
	ID   string
	At   time.Time
	Kind IncomeKind
	// Currency is the ISO code the payment was made in, upper case.
	Currency string
	// Amount is signed: positive came into the account, negative left it (a
	// tax withheld). Never zero.
	Amount decimal.Decimal
	// Raw is the broker's record of the operation as received.
	Raw json.RawMessage
}

// BrokerIncomeReader reads the income history of the one broker account a
// BrokerSyncer was built for. A syncer that can implements it too.
type BrokerIncomeReader interface {
	// ReadIncome returns the executed income operations dated in [from, to);
	// a zero from reads the account's whole history. It returns all of them
	// or an error: a partial answer would read as "nothing else was paid".
	ReadIncome(ctx context.Context, from, to time.Time) ([]BrokerIncome, error)
}
