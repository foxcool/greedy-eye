package tinvest

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

const pathOperationsByCursor = "/tinkoff.public.invest.api.contract.v1.OperationsService/GetOperationsByCursor"

// operationsPageLimit is the most the API returns per page.
const operationsPageLimit = 1000

// maxOperationPages bounds one read. A million income operations is not a
// person's account; a cursor that never ends is a broken source.
const maxOperationPages = 1000

const operationStateExecuted = "OPERATION_STATE_EXECUTED"

// incomeOperationTypes are the operation types that are income, or a tax on
// it, and the income kind each is filed under. The sign of the payment says
// which way the money went, so a tax correction refunding a tax is income in.
//
// Left out on purpose: trades, fees (the stamp duty OUT_STAMP_DUTY among
// them), deposits and withdrawals; the accrued interest paid when buying a
// bond (kept out until trades are in the ledger); bond amortization and
// redemption, whose principal the snapshot already holds.
var incomeOperationTypes = map[string]entity.IncomeKind{
	"OPERATION_TYPE_COUPON":                entity.IncomeCoupon,
	"OPERATION_TYPE_BOND_TAX":              entity.IncomeCoupon,
	"OPERATION_TYPE_BOND_TAX_PROGRESSIVE":  entity.IncomeCoupon,
	"OPERATION_TYPE_TAX_CORRECTION_COUPON": entity.IncomeCoupon,

	"OPERATION_TYPE_DIVIDEND":                 entity.IncomeDividend,
	"OPERATION_TYPE_DIV_EXT":                  entity.IncomeDividend,
	"OPERATION_TYPE_DIVIDEND_TAX":             entity.IncomeDividend,
	"OPERATION_TYPE_DIVIDEND_TAX_PROGRESSIVE": entity.IncomeDividend,

	// Overnight is the broker's interest for lending out the account's idle
	// cash and paper through repo; the repo taxes are the tax on it.
	"OPERATION_TYPE_OVERNIGHT":                   entity.IncomeInterest,
	"OPERATION_TYPE_TAX_REPO":                    entity.IncomeInterest,
	"OPERATION_TYPE_TAX_REPO_PROGRESSIVE":        entity.IncomeInterest,
	"OPERATION_TYPE_TAX_REPO_HOLD":               entity.IncomeInterest,
	"OPERATION_TYPE_TAX_REPO_HOLD_PROGRESSIVE":   entity.IncomeInterest,
	"OPERATION_TYPE_TAX_REPO_REFUND":             entity.IncomeInterest,
	"OPERATION_TYPE_TAX_REPO_REFUND_PROGRESSIVE": entity.IncomeInterest,

	// Taxes that name no income: the annual withholding (on coupons and
	// sales alike), its corrections, and the tax on a material benefit. They
	// are filed as tax so the net matches the broker's.
	"OPERATION_TYPE_TAX":                        entity.IncomeTax,
	"OPERATION_TYPE_TAX_PROGRESSIVE":            entity.IncomeTax,
	"OPERATION_TYPE_TAX_CORRECTION":             entity.IncomeTax,
	"OPERATION_TYPE_TAX_CORRECTION_PROGRESSIVE": entity.IncomeTax,
	"OPERATION_TYPE_BENEFIT_TAX":                entity.IncomeTax,
	"OPERATION_TYPE_BENEFIT_TAX_PROGRESSIVE":    entity.IncomeTax,
}

// IncomeKindOf is the income kind an operation type is filed under; false
// for a type that is not income.
func IncomeKindOf(operationType string) (entity.IncomeKind, bool) {
	k, ok := incomeOperationTypes[operationType]
	return k, ok
}

// operationItem is the part of an OperationItem the ledger reads.
type operationItem struct {
	ID            string      `json:"id"`
	Date          time.Time   `json:"date"`
	Type          string      `json:"type"`
	State         string      `json:"state"`
	InstrumentUID string      `json:"instrumentUid"`
	FIGI          string      `json:"figi"`
	Payment       *MoneyValue `json:"payment"`
}

type operationsPage struct {
	HasNext    bool              `json:"hasNext"`
	NextCursor string            `json:"nextCursor"`
	Items      []json.RawMessage `json:"items"`
}

// operationsRequest is GetOperationsByCursorRequest. From and To are omitted
// when unset: an absent from reads from the account's opening.
type operationsRequest struct {
	AccountID         string     `json:"accountId"`
	From              *time.Time `json:"from,omitempty"`
	To                *time.Time `json:"to,omitempty"`
	Cursor            string     `json:"cursor,omitempty"`
	Limit             int        `json:"limit"`
	OperationTypes    []string   `json:"operationTypes"`
	State             string     `json:"state"`
	WithoutCommission bool       `json:"withoutCommissions"`
	WithoutTrades     bool       `json:"withoutTrades"`
}

// ReadIncome implements entity.BrokerIncomeReader.
func (s *BrokerSyncer) ReadIncome(ctx context.Context, from, to time.Time) ([]entity.BrokerIncome, error) {
	if s.accountID == "" {
		return nil, fmt.Errorf("tinvest: operations: no broker account id")
	}
	req := operationsRequest{
		AccountID:      s.accountID,
		Limit:          operationsPageLimit,
		OperationTypes: slices.Sorted(maps.Keys(incomeOperationTypes)),
		State:          operationStateExecuted,
		// Income carries no commission and no trades; asking for neither
		// keeps the pages small.
		WithoutCommission: true,
		WithoutTrades:     true,
	}
	if !from.IsZero() {
		f := from.UTC()
		req.From = &f
	}
	if !to.IsZero() {
		t := to.UTC()
		req.To = &t
	}

	var out []entity.BrokerIncome
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page == maxOperationPages {
			return nil, fmt.Errorf("tinvest: operations: more than %d pages; the cursor does not end", maxOperationPages)
		}
		var resp operationsPage
		if err := s.client.post(ctx, pathOperationsByCursor, req, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Items {
			inc, ok, err := incomeOf(raw, from, to)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, inc)
			}
		}
		if !resp.HasNext {
			return out, nil
		}
		if resp.NextCursor == "" || seen[resp.NextCursor] {
			return nil, fmt.Errorf("tinvest: operations: the source says there is more but gave no new cursor")
		}
		seen[resp.NextCursor] = true
		req.Cursor = resp.NextCursor
	}
}

// incomeOf reads one operation. An income operation that cannot be read fails
// the read: dropping it would understate the income with nothing to show.
// One that is not income, or not executed, or outside [from, to), is passed
// over whatever the source sent.
func incomeOf(raw json.RawMessage, from, to time.Time) (entity.BrokerIncome, bool, error) {
	var op operationItem
	if err := json.Unmarshal(raw, &op); err != nil {
		return entity.BrokerIncome{}, false, fmt.Errorf("tinvest: decode operation: %w", err)
	}
	kind, ok := incomeOperationTypes[op.Type]
	if !ok || op.State != operationStateExecuted {
		return entity.BrokerIncome{}, false, nil
	}
	if (!from.IsZero() && op.Date.Before(from)) || (!to.IsZero() && !op.Date.Before(to)) {
		return entity.BrokerIncome{}, false, nil
	}
	if op.ID == "" || op.Date.IsZero() {
		return entity.BrokerIncome{}, false, fmt.Errorf("tinvest: %s operation without an id or a date", op.Type)
	}
	// A payment with no currency is not a payment of zero: it is one this
	// adapter cannot read, and passing over it would understate the income.
	if op.Payment == nil || strings.TrimSpace(op.Payment.Currency) == "" {
		return entity.BrokerIncome{}, false, fmt.Errorf("tinvest: operation %s: no payment", op.ID)
	}
	amount, ok := op.Payment.Decimal()
	if !ok {
		return entity.BrokerIncome{}, false, fmt.Errorf("tinvest: operation %s: unreadable payment", op.ID)
	}
	if amount.IsZero() {
		return entity.BrokerIncome{}, false, nil // nothing moved
	}
	code := strings.ToUpper(strings.TrimSpace(op.Payment.Currency))
	if !settlementCurrencies[code] {
		return entity.BrokerIncome{}, false, fmt.Errorf("tinvest: operation %s: payment in %q, not a currency this adapter knows", op.ID, op.Payment.Currency)
	}
	instrument := op.InstrumentUID
	if instrument == "" {
		instrument = op.FIGI
	}
	return entity.BrokerIncome{
		ID:         op.ID,
		At:         op.Date,
		Kind:       kind,
		Instrument: instrument,
		Currency:   code,
		Amount:     amount,
		Raw:        raw,
	}, true, nil
}
