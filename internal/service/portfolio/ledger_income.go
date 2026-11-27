package portfolio

import (
	"context"
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
func (h *Handler) writeBrokerIncome(ctx context.Context, accountID, source string, reader entity.BrokerIncomeReader, from, to time.Time) (int32, error) {
	ops, err := reader.ReadIncome(ctx, from, to)
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
		if _, err := h.store.CreateLedgerEvent(ctx, brokerIncomeEvent(accountID, source, assetID, op)); err != nil {
			if errors.Is(err, store.ErrAlreadyExists) {
				continue
			}
			return created, fmt.Errorf("write operation %s: %w", op.ID, err)
		}
		created++
	}
	return created, nil
}

// brokerIncomeEvent is one operation as an income event: a payment is an in
// leg, a tax withheld on it an out leg of the same income kind.
func brokerIncomeEvent(accountID, source, assetID string, op entity.BrokerIncome) *entity.LedgerEvent {
	dir := entity.LedgerIn
	if op.Amount.IsNegative() {
		dir = entity.LedgerOut
	}
	return &entity.LedgerEvent{
		AccountID:   accountID,
		Kind:        entity.LedgerKindIncome,
		IncomeKind:  op.Kind,
		ExecutedAt:  op.At,
		ExternalRef: op.ID,
		Source:      source,
		Raw:         op.Raw,
		Legs: []entity.LedgerLeg{{
			AssetID:   assetID,
			Direction: dir,
			Role:      entity.LedgerRoleIncome,
			Amount:    op.Amount.Abs().Shift(brokerIncomeDecimals).Truncate(0),
			Decimals:  brokerIncomeDecimals,
		}},
	}
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
