package portfolio

import (
	"cmp"
	"slices"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// incomePrice answers the unit price of an asset in the display currency on
// the date of an event, and false when there is none. Which price stands for
// a date is the caller's policy (see incomePrices); the report only applies it.
type incomePrice func(assetID string, at time.Time) (decimal.Decimal, bool)

// incomeRowKey is what one report row groups: a month, an income kind, the
// asset that paid (empty when none is known) and the asset paid in.
type incomeRowKey struct {
	month   time.Time
	kind    entity.IncomeKind
	payerID string
	assetID string
}

// incomeRow is the sums of one month x income kind x payer x asset. Amounts
// are in the asset's units, values in the display currency; a value only
// counts events that had a price, the amount counts all of them.
type incomeRow struct {
	incomeRowKey
	decimals             uint32
	gross, tax           decimal.Decimal
	grossValue, taxValue decimal.Decimal
	unvaluedEvents       int
}

func (r incomeRow) net() decimal.Decimal { return r.gross.Sub(r.tax) }

// unvaluedIncome is an event's movement in one asset that had no price on the
// event's date.
type unvaluedIncome struct {
	eventID    string
	executedAt time.Time
	assetID    string
}

// incomeReport is what buildIncomeReport derives from a period's events.
type incomeReport struct {
	rows     []incomeRow
	unvalued []unvaluedIncome
}

// buildIncomeReport sums income events into rows of month (UTC) x income kind
// x paying asset x asset paid in. An in leg is a payment and counts as gross; an out leg is what was
// withheld from it and counts as tax, so net = gross - tax. On a tax that
// names no income (kind tax) nothing was paid: an in leg is a refund and
// lowers the tax, which goes below zero when refunds outweigh what the
// period withheld.
//
// An event with no price on its date still adds to the amounts — it was paid —
// but not to any value, and it is listed as unvalued: an unknown price is not
// spent as zero. Events that are not income are ignored.
func buildIncomeReport(events []*entity.LedgerEvent, price incomePrice) incomeReport {
	rows := map[incomeRowKey]*incomeRow{}
	var report incomeReport

	for _, e := range events {
		if e.Kind != entity.LedgerKindIncome {
			continue
		}
		month := time.Date(e.ExecutedAt.UTC().Year(), e.ExecutedAt.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
		unvaluedAssets := map[string]bool{}

		for _, l := range e.Legs {
			key := incomeRowKey{month: month, kind: e.ReportedIncomeKind(), payerID: e.PayerAssetID, assetID: l.AssetID}
			row := rows[key]
			if row == nil {
				row = &incomeRow{incomeRowKey: key}
				rows[key] = row
			}
			row.decimals = max(row.decimals, l.Decimals)

			amount := l.Amount.Shift(-entity.DecI32(l.Decimals))
			unit, priced := price(l.AssetID, e.ExecutedAt)
			value := amount.Mul(unit)

			switch {
			case l.Direction == entity.LedgerIn && key.kind == entity.IncomeTax:
				row.tax = row.tax.Sub(amount)
				if priced {
					row.taxValue = row.taxValue.Sub(value)
				}
			case l.Direction == entity.LedgerIn:
				row.gross = row.gross.Add(amount)
				if priced {
					row.grossValue = row.grossValue.Add(value)
				}
			case l.Direction == entity.LedgerOut:
				row.tax = row.tax.Add(amount)
				if priced {
					row.taxValue = row.taxValue.Add(value)
				}
			}
			if !priced && !unvaluedAssets[l.AssetID] {
				unvaluedAssets[l.AssetID] = true
				row.unvaluedEvents++
				report.unvalued = append(report.unvalued, unvaluedIncome{
					eventID: e.ID, executedAt: e.ExecutedAt, assetID: l.AssetID,
				})
			}
		}
	}

	for _, row := range rows {
		report.rows = append(report.rows, *row)
	}
	slices.SortFunc(report.rows, func(a, b incomeRow) int {
		return cmp.Or(
			a.month.Compare(b.month),
			cmp.Compare(a.kind, b.kind),
			cmp.Compare(a.payerID, b.payerID),
			cmp.Compare(a.assetID, b.assetID),
		)
	})
	return report
}
