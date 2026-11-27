package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
)

// incomeRereadWindow is how far before the last complete read the next one
// starts. An operation the broker books late, under an earlier date, is still
// found; what was written already is refused by its ref.
const incomeRereadWindow = 14 * 24 * time.Hour

// brokerIncomeDecimals is the scale income legs are written at: a payment
// arrives as units and billionths, and nine places carry it exactly.
const brokerIncomeDecimals = 9

// readBrokerIncome continues a broker account's income history into the
// ledger, right after its balances were written. The first run reads the
// whole history.
//
// Best effort: a failure here never fails the balance sync. It leaves the
// cursor where it was, is stored on it for account health, and is returned
// for the sync response. Events written before the failure stay; their refs
// make the retry write nothing twice.
func (h *Handler) readBrokerIncome(ctx context.Context, account *entity.Account) ledgerOutcome {
	if h.brokerSource == nil {
		return ledgerOutcome{}
	}
	syncer, err := h.brokerSource.BrokerSyncerForAccount(account)
	if err != nil || syncer == nil {
		return ledgerOutcome{} // the balance sync has reported it already
	}
	reader, ok := syncer.(entity.BrokerIncomeReader)
	if !ok {
		return ledgerOutcome{}
	}
	source := entity.LedgerSyncSource(strings.TrimSpace(account.Data[providerDataKey]))

	cursor, err := h.accountLedgerCursor(ctx, account.ID)
	if err != nil {
		return ledgerOutcome{errors: []string{fmt.Sprintf("income history: %v", err)}}
	}
	if cursor == nil {
		cursor = &entity.LedgerCursor{AccountID: account.ID, Source: source}
	}

	var out ledgerOutcome
	to := time.Now().UTC()
	if cursor.Source != source {
		err = fmt.Errorf("history here was read from %s, %s would record it twice", cursor.Source, source)
	} else {
		var from time.Time
		if !cursor.AdvancedAt.IsZero() {
			from = cursor.AdvancedAt.Add(-incomeRereadWindow)
		}
		out.eventsCreated, err = h.writeBrokerIncome(ctx, account.ID, source, reader, from, to)
	}

	cursor.CheckedAt = time.Now()
	if err != nil {
		cursor.LastError = err.Error()
		out.errors = append(out.errors, fmt.Sprintf("income history: %v", err))
		h.log.Warn("broker income not read into the ledger",
			"account_id", account.ID, "read_through", cursor.AdvancedAt, "error", err)
	} else {
		cursor.LastError, cursor.AdvancedAt = "", to
	}
	if serr := h.store.SaveLedgerCursor(ctx, cursor); serr != nil {
		out.errors = append(out.errors, fmt.Sprintf("income history: cursor not saved: %v", serr))
	}
	return out
}

// accountLedgerCursor is the account's one chain-less cursor, nil before the
// first read.
func (h *Handler) accountLedgerCursor(ctx context.Context, accountID string) (*entity.LedgerCursor, error) {
	cursors, err := h.store.ListLedgerCursors(ctx, accountID)
	if err != nil {
		return nil, err
	}
	for _, c := range cursors {
		if c.Chain == "" {
			return c, nil
		}
	}
	return nil, nil
}

// writeBrokerIncome reads [from, to) and writes one income event per
// operation. Any failure a retry could get past is returned.
//
// The ref is the operation id, which the broker says may change over time. A
// payment already recorded under another id (same kind, paper, moment, asset,
// direction and amount) is the same payment and is not written again.
func (h *Handler) writeBrokerIncome(ctx context.Context, accountID, source string, reader entity.BrokerIncomeReader, from, to time.Time) (int32, error) {
	ops, err := reader.ReadIncome(ctx, from, to)
	if err != nil {
		return 0, err
	}
	recorded, err := h.recordedIncome(ctx, accountID, source, from, to)
	if err != nil {
		return 0, err
	}
	assets := map[string]string{} // currency → asset id
	var created int32
	for _, op := range ops {
		assetID, ok := assets[op.Currency]
		if !ok {
			if assetID, err = h.currencyAsset(ctx, op.Currency); err != nil {
				return created, fmt.Errorf("resolve %s: %w", op.Currency, err)
			}
			assets[op.Currency] = assetID
		}
		event, err := brokerIncomeEvent(accountID, source, assetID, op)
		if err != nil {
			return created, err
		}
		key := incomeKey(event, op.Instrument)
		if ref, ok := recorded[key]; ok {
			if ref != op.ID {
				h.log.Info("broker income recorded under another operation id; not written again",
					"account_id", accountID, "recorded_ref", ref, "ref", op.ID)
			}
			continue
		}
		if _, err := h.store.CreateLedgerEvent(ctx, event); err != nil {
			if errors.Is(err, store.ErrAlreadyExists) {
				continue
			}
			return created, fmt.Errorf("write operation %s: %w", op.ID, err)
		}
		recorded[key] = op.ID
		created++
	}
	return created, nil
}

// incomeRaw is what a broker income event keeps in raw: the paper that paid,
// which the event has no column for, beside the operation as received.
type incomeRaw struct {
	Instrument string          `json:"instrument,omitempty"`
	Operation  json.RawMessage `json:"operation"`
}

// recordedIncome maps the content key of every income event this source
// recorded for the account in [from, to) to its ref.
func (h *Handler) recordedIncome(ctx context.Context, accountID, source string, from, to time.Time) (map[string]string, error) {
	out := map[string]string{}
	opts := ListLedgerOpts{AccountID: accountID, Kind: entity.LedgerKindIncome, ExecutedFrom: from, ExecutedTo: to}
	for {
		page, next, err := h.store.ListLedgerEvents(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("read recorded income: %w", err)
		}
		for _, e := range page {
			if e.Source != source {
				continue
			}
			var raw incomeRaw
			_ = json.Unmarshal(e.Raw, &raw) // an unreadable raw names no paper
			out[incomeKey(e, raw.Instrument)] = e.ExternalRef
		}
		if next == "" || len(page) == 0 || next == opts.PageToken {
			return out, nil
		}
		opts.PageToken = next
	}
}

// incomeKey is what makes two income events one payment when their refs
// differ. The paper is part of it: two bonds paying the same sum at the same
// moment are two payments.
func incomeKey(e *entity.LedgerEvent, instrument string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s", e.IncomeKind, instrument, e.ExecutedAt.UTC().Format(time.RFC3339Nano))
	for _, l := range e.Legs {
		fmt.Fprintf(&b, "|%s:%s:%s/%d", l.AssetID, l.Direction, l.Amount.String(), l.Decimals)
	}
	return b.String()
}

// brokerIncomeEvent is one operation as an income event: a payment is an in
// leg, a tax withheld on it an out leg of the same income kind.
func brokerIncomeEvent(accountID, source, assetID string, op entity.BrokerIncome) (*entity.LedgerEvent, error) {
	dir := entity.LedgerIn
	if op.Amount.IsNegative() {
		dir = entity.LedgerOut
	}
	raw, err := json.Marshal(incomeRaw{Instrument: op.Instrument, Operation: op.Raw})
	if err != nil {
		return nil, fmt.Errorf("encode raw of %s: %w", op.ID, err)
	}
	return &entity.LedgerEvent{
		AccountID:  accountID,
		Kind:       entity.LedgerKindIncome,
		IncomeKind: op.Kind,
		// The database keeps microseconds; the content key must match what it
		// reads back.
		ExecutedAt:  op.At.UTC().Truncate(time.Microsecond),
		ExternalRef: op.ID,
		Source:      source,
		Raw:         raw,
		Legs: []entity.LedgerLeg{{
			AssetID:   assetID,
			Direction: dir,
			Role:      entity.LedgerRoleIncome,
			Amount:    op.Amount.Abs().Shift(brokerIncomeDecimals).Truncate(0),
			Decimals:  brokerIncomeDecimals,
		}},
	}, nil
}

// currencyAsset finds the asset of a currency the way a broker sync finds the
// asset of its cash line.
func (h *Handler) currencyAsset(ctx context.Context, code string) (string, error) {
	if h.mdClient == nil {
		return "", errors.New("asset catalogue not configured")
	}
	id, _, _, err := h.resolveSyncedAsset(ctx, syncedBalance{
		symbol:    code,
		name:      code,
		assetType: entity.AssetTypeForex,
		market:    entity.DefaultMarket(entity.AssetTypeForex),
	})
	return id, err
}
