package portfolio

import (
	"testing"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func incomeEvent(id string, at time.Time, kind entity.IncomeKind, legs ...entity.LedgerLeg) *entity.LedgerEvent {
	return &entity.LedgerEvent{ID: id, Kind: entity.LedgerKindIncome, IncomeKind: kind, ExecutedAt: at, Legs: legs}
}

func incomeLeg(asset string, dir entity.LedgerDirection, amount string, decimals uint32) entity.LedgerLeg {
	return entity.LedgerLeg{
		AssetID: asset, Direction: dir, Role: entity.LedgerRoleIncome,
		Amount: decimal.RequireFromString(amount).Shift(int32(decimals)), Decimals: decimals,
	}
}

func fixedPrice(prices map[string]string) incomePrice {
	return func(assetID string, _ time.Time) (decimal.Decimal, bool) {
		p, ok := prices[assetID]
		if !ok {
			return decimal.Zero, false
		}
		return decimal.RequireFromString(p), true
	}
}

// A coupon of 1000 with 130 withheld nets 870: the tax is its own event with
// an out leg, and the report shows all three numbers rather than only the net
// that runway needs.
func TestBuildIncomeReport_TaxIsWithheldFromGross(t *testing.T) {
	paid := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	events := []*entity.LedgerEvent{
		incomeEvent("payout", paid, entity.IncomeCoupon, incomeLeg("rub", entity.LedgerIn, "1000", 2)),
		incomeEvent("tax", paid, entity.IncomeCoupon, incomeLeg("rub", entity.LedgerOut, "130", 2)),
	}

	report := buildIncomeReport(events, fixedPrice(map[string]string{"rub": "0.01"}))

	require.Len(t, report.rows, 1)
	row := report.rows[0]
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), row.month)
	assert.Equal(t, entity.IncomeCoupon, row.kind)
	assert.Equal(t, "1000", row.gross.String())
	assert.Equal(t, "130", row.tax.String())
	assert.Equal(t, "870", row.net().String())
	assert.Equal(t, "10", row.grossValue.String())
	assert.Equal(t, "1.3", row.taxValue.String())
	assert.Empty(t, report.unvalued)
}

// An event with no price on its date was still paid: it counts in the amount,
// stays out of every value, and is named, never valued at zero.
func TestBuildIncomeReport_UnpricedEventStaysOutOfValues(t *testing.T) {
	at := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	events := []*entity.LedgerEvent{
		incomeEvent("dot", at, entity.IncomeStakingReward, incomeLeg("dot", entity.LedgerIn, "2", 10)),
		incomeEvent("myth", at, entity.IncomeStakingReward, incomeLeg("myth", entity.LedgerIn, "50", 10)),
	}

	report := buildIncomeReport(events, fixedPrice(map[string]string{"dot": "4"}))

	require.Len(t, report.unvalued, 1)
	assert.Equal(t, "myth", report.unvalued[0].eventID)

	require.Len(t, report.rows, 2)
	assert.Equal(t, "8", report.rows[0].grossValue.String(), "the priced reward is valued")
	myth := report.rows[1]
	assert.Equal(t, "myth", myth.assetID)
	assert.Equal(t, "50", myth.gross.String(), "the amount was paid whatever its price")
	assert.True(t, myth.grossValue.IsZero())
	assert.Equal(t, 1, myth.unvaluedEvents)
}

// Rows are month x kind x asset; months are UTC calendar months, and an income
// event written before income kinds existed is filed as other.
func TestBuildIncomeReport_GroupsByMonthKindAsset(t *testing.T) {
	legacy := incomeEvent("legacy", time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC), "",
		incomeLeg("usdt", entity.LedgerIn, "1", 6))
	events := []*entity.LedgerEvent{
		incomeEvent("a", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), entity.IncomeInterest, incomeLeg("usdt", entity.LedgerIn, "2", 6)),
		incomeEvent("b", time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), entity.IncomeInterest, incomeLeg("usdt", entity.LedgerIn, "3", 6)),
		legacy,
		{ID: "trade", Kind: entity.LedgerKindTrade, ExecutedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
			Legs: []entity.LedgerLeg{incomeLeg("usdt", entity.LedgerIn, "100", 6)}},
	}

	report := buildIncomeReport(events, fixedPrice(map[string]string{"usdt": "1"}))

	require.Len(t, report.rows, 2, "the trade is not income")
	assert.Equal(t, entity.IncomeOther, report.rows[0].kind)
	assert.Equal(t, time.September, report.rows[0].month.Month())
	assert.Equal(t, entity.IncomeInterest, report.rows[1].kind)
	assert.Equal(t, "5", report.rows[1].gross.String())
}

// The rate in force on a date is the last one set at or before it: a rate
// published the next morning does not reach back, and a holiday keeps the
// previous rate in force rather than leaving the date unvalued.
func TestRateInForce(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 21, 0, 0, 0, time.UTC) }
	rates := []datedPrice{
		{at: day(20), unit: decimal.RequireFromString("0.0120")},
		{at: day(23), unit: decimal.RequireFromString("0.0123")},
	}

	got, ok := rateInForce(rates, day(23).Add(-time.Minute))
	require.True(t, ok)
	assert.Equal(t, "0.012", got.String(), "the 23rd's rate is not in force a minute before it is set")

	got, ok = rateInForce(rates, day(25))
	require.True(t, ok)
	assert.Equal(t, "0.0123", got.String())

	_, ok = rateInForce(rates, day(19))
	assert.False(t, ok, "no rate was set before the first one")

	got, ok = rateInForce(rates, day(23).Add(12*24*time.Hour))
	require.True(t, ok, "the New Year holiday keeps the last rate in force")
	assert.Equal(t, "0.0123", got.String())

	_, ok = rateInForce(rates, day(23).Add(45*24*time.Hour))
	assert.False(t, ok, "a gap this long is missing data, not a rate still in force")
}

func TestNearestPrice(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	prices := []datedPrice{
		{at: at.Add(-48 * time.Hour), unit: decimal.NewFromInt(1)},
		{at: at.Add(30 * time.Hour), unit: decimal.NewFromInt(2)},
	}
	got, ok := nearestPrice(prices, at, incomePriceWindow)
	require.True(t, ok)
	assert.Equal(t, "2", got.String(), "the closer price wins, before or after")

	_, ok = nearestPrice(prices, at.Add(10*24*time.Hour), incomePriceWindow)
	assert.False(t, ok, "nothing within the window is no price, not the last known one")
}

// A synced account takes income only, and not the income its own sync
// records; a manual account takes everything.
func TestRefuseLedgerImport(t *testing.T) {
	coupon := &entity.LedgerEvent{Kind: entity.LedgerKindIncome, IncomeKind: entity.IncomeCoupon}
	interest := &entity.LedgerEvent{Kind: entity.LedgerKindIncome, IncomeKind: entity.IncomeInterest}
	reward := &entity.LedgerEvent{Kind: entity.LedgerKindIncome, IncomeKind: entity.IncomeStakingReward}
	trade := &entity.LedgerEvent{Kind: entity.LedgerKindTrade}

	manual := &entity.Account{Type: entity.AccountTypeManual}
	broker := &entity.Account{Type: entity.AccountTypeBroker}
	exchange := &entity.Account{Type: entity.AccountTypeExchange}

	for _, e := range []*entity.LedgerEvent{coupon, trade} {
		assert.NoError(t, refuseLedgerImport(manual, e))
	}
	assert.Error(t, refuseLedgerImport(broker, coupon), "the broker sync records coupons")
	assert.Error(t, refuseLedgerImport(broker, interest), "and broker interest")
	tax := &entity.LedgerEvent{Kind: entity.LedgerKindIncome, IncomeKind: entity.IncomeTax}
	assert.Error(t, refuseLedgerImport(broker, tax), "and the taxes that name no income")
	assert.NoError(t, refuseLedgerImport(manual, tax))
	assert.NoError(t, refuseLedgerImport(broker, reward))
	assert.NoError(t, refuseLedgerImport(exchange, interest), "exchange earn is imported by hand")
	assert.Error(t, refuseLedgerImport(exchange, trade), "trades on a synced account are its sync's")
}

// Every value in the response is added up from cents already rounded per row,
// so a net is its gross minus its tax and a total is the sum of its rows. Rounded
// separately, 4.99296 + 1.211408 - 0.15748 gave rows of 6.04 under a total of 6.05.
func TestIncomeReportToProto_TotalsAreSumsOfRows(t *testing.T) {
	report := incomeReport{rows: []incomeRow{
		{incomeRowKey: incomeRowKey{assetID: "usdt"}, decimals: 6, gross: decimal.NewFromInt(5),
			grossValue: decimal.RequireFromString("4.99296")},
		{incomeRowKey: incomeRowKey{assetID: "rub"}, decimals: 2, gross: decimal.NewFromInt(100), tax: decimal.NewFromInt(13),
			grossValue: decimal.RequireFromString("1.211408"), taxValue: decimal.RequireFromString("0.15748")},
	}}

	out := incomeReportToProto(report, nil, "usd")

	assert.Equal(t, "499", out.Rows[0].NetValue)
	assert.Equal(t, "121", out.Rows[1].GrossValue)
	assert.Equal(t, "16", out.Rows[1].TaxValue)
	assert.Equal(t, "105", out.Rows[1].NetValue, "net is gross minus tax in cents")
	assert.Equal(t, "604", out.NetValue, "the total is the sum of the rows")
}

// Two payments without an id, same date and amount, different income kinds,
// are two events: dedup must not fold the second into the first.
func TestLedgerHeuristicKey_IncomeKindIsIdentity(t *testing.T) {
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	coupon := incomeEvent("", at, entity.IncomeCoupon, incomeLeg("rub", entity.LedgerIn, "100", 2))
	dividend := incomeEvent("", at, entity.IncomeDividend, incomeLeg("rub", entity.LedgerIn, "100", 2))
	legacy := incomeEvent("", at, "", incomeLeg("rub", entity.LedgerIn, "100", 2))
	other := incomeEvent("", at, entity.IncomeOther, incomeLeg("rub", entity.LedgerIn, "100", 2))

	assert.NotEqual(t, ledgerHeuristicKey(coupon), ledgerHeuristicKey(dividend))
	assert.Equal(t, ledgerHeuristicKey(legacy), ledgerHeuristicKey(other),
		"an income event stored before kinds existed is the same event re-imported as other")
}

// TestIncomeKindsReachTheAPI: every income kind the ledger accepts has an API
// value and comes back from it, so no stored event reads as unspecified.
func TestIncomeKindsReachTheAPI(t *testing.T) {
	for _, k := range []entity.IncomeKind{entity.IncomeCoupon, entity.IncomeDividend, entity.IncomeInterest,
		entity.IncomeStakingReward, entity.IncomeOther, entity.IncomeTax} {
		require.True(t, k.Valid(), k)
		p, ok := incomeKindToProto[k]
		require.True(t, ok, k)
		assert.Equal(t, k, protoIncomeKind(p), k)
	}
	assert.Equal(t, apiv1.IncomeKind_INCOME_KIND_TAX, incomeKindToProto[entity.IncomeTax])
}
