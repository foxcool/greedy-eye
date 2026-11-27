//go:build integration

package postgres

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/entity/entitytest"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const incomeLeg = apiv1.LedgerLegRole_LEDGER_LEG_ROLE_INCOME

func incomeItem(kind apiv1.IncomeKind, at time.Time, ref string, legs ...*apiv1.ImportLedgerLeg) *apiv1.ImportTransactionItem {
	return &apiv1.ImportTransactionItem{
		Kind:        apiv1.LedgerEventKind_LEDGER_EVENT_KIND_INCOME,
		IncomeKind:  kind,
		ExecutedAt:  timestamppb.New(at),
		ExternalRef: &ref,
		Legs:        legs,
	}
}

// TestIncomeReport_ManualImport: income imported by hand into a synced
// account the caller owns shows up in the period report, valued at the rate
// in force on the payment date, and the import neither writes twice nor
// touches the account's holdings.
func TestIncomeReport_ManualImport(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := middleware.ContextWithUser(f.ctx, f.user)
	assets := NewMarketDataStore(testDB.Pool)

	usd, err := assets.CreateAsset(f.ctx, &entity.Asset{Symbol: "INCUSD", Name: "Income USD", Type: entity.AssetTypeForex})
	require.NoError(t, err)
	rub, err := assets.CreateAsset(f.ctx, &entity.Asset{Symbol: "INCRUB", Name: "Income RUB", Type: entity.AssetTypeForex})
	require.NoError(t, err)

	// The rouble rate in force on 15 October was set at 00:00 Moscow time.
	rateSet := time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)
	for _, p := range []struct {
		at   time.Time
		rate string
	}{{rateSet, "0.01"}, {rateSet.Add(24 * time.Hour), "0.02"}} {
		_, err := assets.CreatePrice(f.ctx, &entity.StoredPrice{
			SourceID: "cbr", AssetID: rub.ID, BaseAssetID: usd.ID, Interval: "latest",
			Decimals: 8, Last: decimal.RequireFromString(p.rate).Shift(8), Timestamp: p.at,
		})
		require.NoError(t, err)
	}

	exchange, err := f.store.CreateAccount(f.ctx, &entity.Account{UserID: f.user.ID, Name: "binance", Type: entity.AccountTypeExchange})
	require.NoError(t, err)
	broker, err := f.store.CreateAccount(f.ctx, &entity.Account{UserID: f.user.ID, Name: "T-Invest", Type: entity.AccountTypeBroker})
	require.NoError(t, err)
	_, err = f.store.CreateHolding(f.ctx, entitytest.Holding(entity.Holding{
		AssetID: rub.ID, AccountID: exchange.ID, Amount: decimal.NewFromInt(5000), Decimals: 2,
	}, entitytest.HoldingState{Source: entity.SourceSync}))
	require.NoError(t, err)
	holdingsBefore := f.countRows(t, "holdings")

	paid := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	importInto := func(accountID string, items ...*apiv1.ImportTransactionItem) *apiv1.ImportTransactionsResponse {
		resp, err := f.handler.ImportTransactions(ctx, connect.NewRequest(&apiv1.ImportTransactionsRequest{
			AccountId: accountID, Transactions: items,
		}))
		require.NoError(t, err)
		return resp.Msg
	}
	batch := func() []*apiv1.ImportTransactionItem {
		return []*apiv1.ImportTransactionItem{
			incomeItem(apiv1.IncomeKind_INCOME_KIND_INTEREST, paid, "earn-1", importLeg(rub.ID, "1000", legIn, incomeLeg)),
			incomeItem(apiv1.IncomeKind_INCOME_KIND_INTEREST, paid, "earn-1-tax", importLeg(rub.ID, "130", legOut, incomeLeg)),
		}
	}

	first := importInto(exchange.ID, batch()...)
	assert.Equal(t, int32(2), first.Created, "%v", first.Items)
	again := importInto(exchange.ID, batch()...)
	assert.Zero(t, again.Created, "re-importing the statement writes nothing")
	assert.Equal(t, holdingsBefore, f.countRows(t, "holdings"), "income never writes holdings")

	// The broker sync owns coupons: refused per item, the rest of the batch lands.
	mixed := importInto(broker.ID,
		incomeItem(apiv1.IncomeKind_INCOME_KIND_COUPON, paid, "coupon-1", importLeg(rub.ID, "50", legIn, incomeLeg)),
		incomeItem(apiv1.IncomeKind_INCOME_KIND_OTHER, paid, "bonus-1", importLeg(rub.ID, "10", legIn, incomeLeg)),
	)
	assert.Equal(t, int32(1), mixed.Failed)
	assert.Equal(t, int32(1), mixed.Created)
	require.NotNil(t, mixed.Items[0].Error)
	assert.Contains(t, *mixed.Items[0].Error, "recorded by its sync")

	report, err := f.handler.GetIncomeReport(ctx, connect.NewRequest(&apiv1.GetIncomeReportRequest{
		AccountId:    &exchange.ID,
		From:         timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
		To:           timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
		QuoteAssetId: &usd.ID,
	}))
	require.NoError(t, err)
	require.Len(t, report.Msg.Rows, 1)
	row := report.Msg.Rows[0]
	assert.Equal(t, apiv1.IncomeKind_INCOME_KIND_INTEREST, row.IncomeKind)
	assert.Equal(t, "INCRUB", row.Symbol)
	assert.Equal(t, "870", decimal.RequireFromString(row.NetAmount).Shift(-int32(row.Decimals)).String())
	// 1000 RUB at the 15 October rate of 0.01, not the next day's 0.02.
	assert.Equal(t, "1000", row.GrossValue, "10.00 USD in cents")
	assert.Equal(t, "130", row.TaxValue)
	assert.Equal(t, "870", report.Msg.NetValue)
	assert.Empty(t, report.Msg.Unvalued)

	// Another user gets the answer a foreign holding gets.
	stranger := middleware.ContextWithUser(f.ctx, createTestUser(t, NewUserStore(testDB.Pool)))
	_, err = f.handler.GetIncomeReport(stranger, connect.NewRequest(&apiv1.GetIncomeReportRequest{
		AccountId: &exchange.ID,
		From:      timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
		To:        timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
	}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, foreignHolding := f.handler.GetAccount(stranger, connect.NewRequest(&apiv1.GetAccountRequest{Id: exchange.ID}))
	assert.Equal(t, connect.CodeOf(foreignHolding), connect.CodeOf(err))

	// An admin passes the ownership check of another user's account and must
	// then see its income, not an empty report.
	adminUser := createTestUser(t, NewUserStore(testDB.Pool))
	adminUser.Roles = []string{"admin"}
	asAdmin, err := f.handler.GetIncomeReport(middleware.ContextWithUser(f.ctx, adminUser), connect.NewRequest(&apiv1.GetIncomeReportRequest{
		AccountId:    &exchange.ID,
		From:         timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
		To:           timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
		QuoteAssetId: &usd.ID,
	}))
	require.NoError(t, err)
	assert.Equal(t, report.Msg.NetValue, asAdmin.Msg.NetValue)
}
