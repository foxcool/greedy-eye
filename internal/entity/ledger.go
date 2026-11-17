package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// LedgerEventKind says why an account's assets moved; the legs say what moved.
// The set is mirrored by a CHECK on ledger_events.kind.
type LedgerEventKind string

const (
	LedgerKindTrade       LedgerEventKind = "trade"
	LedgerKindTransferIn  LedgerEventKind = "transfer_in"
	LedgerKindTransferOut LedgerEventKind = "transfer_out"
	LedgerKindDeposit     LedgerEventKind = "deposit"
	LedgerKindWithdrawal  LedgerEventKind = "withdrawal"
	LedgerKindIncome      LedgerEventKind = "income"
	LedgerKindFee         LedgerEventKind = "fee"
	LedgerKindOpening     LedgerEventKind = "opening"
)

// Valid reports whether k is one of the known kinds.
func (k LedgerEventKind) Valid() bool {
	switch k {
	case LedgerKindTrade, LedgerKindTransferIn, LedgerKindTransferOut, LedgerKindDeposit,
		LedgerKindWithdrawal, LedgerKindIncome, LedgerKindFee, LedgerKindOpening:
		return true
	}
	return false
}

// LedgerDirection is whether the account received a leg's amount or gave it up.
type LedgerDirection string

const (
	LedgerIn  LedgerDirection = "in"
	LedgerOut LedgerDirection = "out"
)

// Valid reports whether d is in or out.
func (d LedgerDirection) Valid() bool { return d == LedgerIn || d == LedgerOut }

// Opposite is the other direction; a trade's counter leg moves against its principal.
func (d LedgerDirection) Opposite() LedgerDirection {
	if d == LedgerIn {
		return LedgerOut
	}
	return LedgerIn
}

// LedgerRole is what a leg is to its event.
type LedgerRole string

const (
	LedgerRolePrincipal LedgerRole = "principal"
	LedgerRoleCounter   LedgerRole = "counter"
	LedgerRoleFee       LedgerRole = "fee"
	LedgerRoleIncome    LedgerRole = "income"
)

// Valid reports whether r is one of the known roles.
func (r LedgerRole) Valid() bool {
	switch r {
	case LedgerRolePrincipal, LedgerRoleCounter, LedgerRoleFee, LedgerRoleIncome:
		return true
	}
	return false
}

// ledgerSyncPrefix starts the source of an event a provider sync wrote; the
// provider slug follows it, so the idempotency key of one provider's refs
// cannot collide with another's.
const ledgerSyncPrefix = "sync:"

// LedgerSyncSource is the source of an event written by the named provider's sync.
func LedgerSyncSource(provider string) string { return ledgerSyncPrefix + provider }

// ValidLedgerSource reports whether s is "manual", "llm_import" or "sync:<provider>".
func ValidLedgerSource(s string) bool {
	switch {
	case s == string(SourceManual), s == string(SourceLLMImport):
		return true
	case strings.HasPrefix(s, ledgerSyncPrefix):
		return len(s) > len(ledgerSyncPrefix)
	}
	return false
}

// LedgerEvent is one thing that happened to an account, with every asset
// movement it made as a leg. An operation that moves several assets (a buy
// paid in one coin with a fee in another) is one event, not several rows.
type LedgerEvent struct {
	ID        string
	AccountID string
	Kind      LedgerEventKind
	// ExecutedAt is when the event happened, not when the row was written.
	ExecutedAt time.Time
	// ExternalRef is the source's id for the event; unique per (account, source).
	ExternalRef string
	Source      string
	ImportID    string
	// Raw is the source payload as received; kept for reprocessing, never returned by the API.
	Raw       json.RawMessage
	Legs      []LedgerLeg
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LedgerLeg is one asset movement of an event. Amount is a raw integer scaled
// by Decimals, as on holdings, and never negative: Direction carries the sign.
type LedgerLeg struct {
	ID        string
	EventID   string
	AssetID   string
	Direction LedgerDirection
	Role      LedgerRole
	Amount    decimal.Decimal
	Decimals  uint32
	// Chain is empty when the movement is not chain-scoped (see Holding.Chain).
	Chain string
}

// Validate checks what an event must carry before it is written.
func (e *LedgerEvent) Validate() error {
	switch {
	case e.AccountID == "":
		return errors.New("account_id is required")
	case !e.Kind.Valid():
		return fmt.Errorf("unknown ledger event kind %q", e.Kind)
	case e.ExecutedAt.IsZero():
		return errors.New("executed_at is required")
	case !ValidLedgerSource(e.Source):
		return fmt.Errorf("unknown ledger source %q", e.Source)
	case len(e.Legs) == 0:
		return errors.New("a ledger event needs at least one leg")
	}
	for i, l := range e.Legs {
		switch {
		case l.AssetID == "":
			return fmt.Errorf("leg %d: asset is required", i)
		case !l.Direction.Valid():
			return fmt.Errorf("leg %d: unknown direction %q", i, l.Direction)
		case !l.Role.Valid():
			return fmt.Errorf("leg %d: unknown role %q", i, l.Role)
		case !l.Amount.IsPositive():
			return fmt.Errorf("leg %d: amount must be positive", i)
		case !l.Amount.IsInteger():
			return fmt.Errorf("leg %d: amount must be a raw integer scaled by decimals", i)
		}
	}
	return nil
}
