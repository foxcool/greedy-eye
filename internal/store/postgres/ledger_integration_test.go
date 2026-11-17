//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ledgerFixture is a manual account of one user with two assets, served by
// real handlers over the real store.
type ledgerFixture struct {
	ctx     context.Context
	store   *PortfolioStore
	handler *portfolio.Handler
	user    *entity.User
	account *entity.Account
	btc     *entity.Asset
	usdt    *entity.Asset
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	pool := getTestPool(t)
	ctx := context.Background()
	s := NewPortfolioStore(pool)
	assets := NewMarketDataStore(pool)
	user := createTestUser(t, NewUserStore(pool))
	account, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "export", Type: entity.AccountTypeManual})
	require.NoError(t, err)
	md := marketdata.NewHandler(assets, slog.New(slog.DiscardHandler))
	return &ledgerFixture{
		ctx:     ctx,
		store:   s,
		handler: portfolio.NewHandler(s, slog.New(slog.DiscardHandler)).WithMarketDataClient(md),
		user:    user,
		account: account,
		btc:     createTestAsset(t, assets, "Ledger Import BTC"),
		usdt:    createTestAsset(t, assets, "Ledger Import USDT"),
	}
}

func (f *ledgerFixture) countRows(t *testing.T, table string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.Pool.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func (f *ledgerFixture) importBatch(t *testing.T, items []*apiv1.ImportTransactionItem) *apiv1.ImportTransactionsResponse {
	t.Helper()
	resp, err := f.handler.ImportTransactions(middleware.ContextWithUser(f.ctx, f.user), connect.NewRequest(&apiv1.ImportTransactionsRequest{
		AccountId:    f.account.ID,
		Transactions: items,
	}))
	require.NoError(t, err)
	return resp.Msg
}

func importLeg(assetID, amount string, dir apiv1.LedgerLegDirection, role apiv1.LedgerLegRole) *apiv1.ImportLedgerLeg {
	return &apiv1.ImportLedgerLeg{AssetId: &assetID, Amount: amount, Direction: dir, Role: role}
}

const (
	legIn     = apiv1.LedgerLegDirection_LEDGER_LEG_DIRECTION_IN
	legOut    = apiv1.LedgerLegDirection_LEDGER_LEG_DIRECTION_OUT
	principal = apiv1.LedgerLegRole_LEDGER_LEG_ROLE_PRINCIPAL
	counter   = apiv1.LedgerLegRole_LEDGER_LEG_ROLE_COUNTER
)

// TestImportTransactions_ReimportCreatesNothing: a manual export lands as
// events with legs, and importing the same export again writes no row — the
// rows with an id by their ref, the rows without one by the heuristic.
func TestImportTransactions_ReimportCreatesNothing(t *testing.T) {
	f := newLedgerFixture(t)
	ref := "export-row-1"
	export := func() []*apiv1.ImportTransactionItem {
		return []*apiv1.ImportTransactionItem{
			{
				Kind:        apiv1.LedgerEventKind_LEDGER_EVENT_KIND_DEPOSIT,
				ExecutedAt:  timestamppb.New(time.Date(2026, 2, 1, 9, 30, 0, 0, time.UTC)),
				ExternalRef: &ref,
				Legs:        []*apiv1.ImportLedgerLeg{importLeg(f.usdt.ID, "1000", legIn, principal)},
				Raw:         map[string]string{"row": "1"},
			},
			{
				Kind: apiv1.LedgerEventKind_LEDGER_EVENT_KIND_TRADE,
				// Sub-microsecond digits: the column drops them, the dedup must too.
				ExecutedAt: timestamppb.New(time.Date(2026, 2, 2, 10, 0, 0, 123456789, time.UTC)),
				Legs: []*apiv1.ImportLedgerLeg{
					importLeg(f.btc.ID, "0.01", legIn, principal),
					importLeg(f.usdt.ID, "950.5", legOut, counter),
				},
			},
		}
	}

	first := f.importBatch(t, export())
	assert.Equal(t, int32(2), first.Created, "%v", first.Items)
	assert.Zero(t, first.Failed)
	assert.Equal(t, 2, f.countRows(t, "ledger_events"))
	assert.Equal(t, 3, f.countRows(t, "ledger_legs"))

	again := f.importBatch(t, export())
	assert.Zero(t, again.Created, "%v", again.Items)
	assert.Equal(t, int32(2), again.Skipped)
	assert.Equal(t, 2, f.countRows(t, "ledger_events"))
	assert.Equal(t, 3, f.countRows(t, "ledger_legs"))

	events, _, err := f.store.ListLedgerEvents(f.ctx, portfolio.ListLedgerOpts{AccountID: f.account.ID})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, string(entity.SourceLLMImport), events[0].Source)
	assert.Equal(t, first.ImportId, events[0].ImportID)
	assert.JSONEq(t, `{"row":"1"}`, string(events[0].Raw))
}

// TestImportTransactions_TradeIsOneEventWithTwoLegs: a buy of BTC for USDT is
// one event whose legs move opposite ways.
func TestImportTransactions_TradeIsOneEventWithTwoLegs(t *testing.T) {
	f := newLedgerFixture(t)
	resp := f.importBatch(t, []*apiv1.ImportTransactionItem{{
		Kind:       apiv1.LedgerEventKind_LEDGER_EVENT_KIND_TRADE,
		ExecutedAt: timestamppb.New(time.Date(2026, 2, 2, 10, 0, 0, 0, time.UTC)),
		Legs: []*apiv1.ImportLedgerLeg{
			importLeg(f.btc.ID, "0.0025", legIn, principal),
			importLeg(f.usdt.ID, "100", legOut, counter),
		},
	}})
	require.Equal(t, int32(1), resp.Created, "%v", resp.Items)

	events, _, err := f.store.ListLedgerEvents(f.ctx, portfolio.ListLedgerOpts{AccountID: f.account.ID})
	require.NoError(t, err)
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, resp.Items[0].GetEventId(), ev.ID)
	assert.Equal(t, entity.LedgerKindTrade, ev.Kind)
	require.Len(t, ev.Legs, 2)
	byRole := map[entity.LedgerRole]entity.LedgerLeg{}
	for _, l := range ev.Legs {
		byRole[l.Role] = l
	}
	assert.Equal(t, f.btc.ID, byRole[entity.LedgerRolePrincipal].AssetID)
	assert.Equal(t, entity.LedgerIn, byRole[entity.LedgerRolePrincipal].Direction)
	assert.Equal(t, "250000", byRole[entity.LedgerRolePrincipal].Amount.String(), "0.0025 at 8 decimals")
	assert.Equal(t, f.usdt.ID, byRole[entity.LedgerRoleCounter].AssetID)
	assert.Equal(t, entity.LedgerOut, byRole[entity.LedgerRoleCounter].Direction)
}

// TestListLedger_OwnerOnly: the owner reads the account's ledger, filtered
// and paged; another user naming the same account gets what ListHoldings gives
// them for it — an empty page, not an error that confirms the account exists.
func TestListLedger_OwnerOnly(t *testing.T) {
	f := newLedgerFixture(t)
	day := func(d int) *timestamppb.Timestamp {
		return timestamppb.New(time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC))
	}
	resp := f.importBatch(t, []*apiv1.ImportTransactionItem{
		{Kind: apiv1.LedgerEventKind_LEDGER_EVENT_KIND_DEPOSIT, ExecutedAt: day(1),
			Legs: []*apiv1.ImportLedgerLeg{importLeg(f.usdt.ID, "1000", legIn, principal)},
			Raw:  map[string]string{"memo": "do-not-leak"}},
		{Kind: apiv1.LedgerEventKind_LEDGER_EVENT_KIND_TRADE, ExecutedAt: day(2),
			Legs: []*apiv1.ImportLedgerLeg{importLeg(f.btc.ID, "0.01", legIn, principal), importLeg(f.usdt.ID, "900", legOut, counter)}},
		{Kind: apiv1.LedgerEventKind_LEDGER_EVENT_KIND_WITHDRAWAL, ExecutedAt: day(3),
			Legs: []*apiv1.ImportLedgerLeg{importLeg(f.usdt.ID, "50", legOut, principal)}},
	})
	require.Equal(t, int32(3), resp.Created, "%v", resp.Items)

	owner := middleware.ContextWithUser(f.ctx, f.user)
	list := func(ctx context.Context, req *apiv1.ListLedgerRequest) *apiv1.ListLedgerResponse {
		t.Helper()
		out, err := f.handler.ListLedger(ctx, connect.NewRequest(req))
		require.NoError(t, err)
		return out.Msg
	}

	t.Run("the owner pages through every event, oldest first", func(t *testing.T) {
		size := int32(2)
		first := list(owner, &apiv1.ListLedgerRequest{AccountId: f.account.ID, PageSize: &size})
		require.Len(t, first.Events, 2)
		require.NotEmpty(t, first.NextPageToken)
		second := list(owner, &apiv1.ListLedgerRequest{AccountId: f.account.ID, PageSize: &size, PageToken: &first.NextPageToken})
		require.Len(t, second.Events, 1)
		assert.Empty(t, second.NextPageToken)
		assert.Equal(t, apiv1.LedgerEventKind_LEDGER_EVENT_KIND_DEPOSIT, first.Events[0].Kind)
		assert.Equal(t, apiv1.LedgerEventKind_LEDGER_EVENT_KIND_WITHDRAWAL, second.Events[0].Kind)

		body, err := protojson.Marshal(first)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "do-not-leak", "raw is not part of the response")
	})

	t.Run("asset and time filters", func(t *testing.T) {
		btcOnly := list(owner, &apiv1.ListLedgerRequest{AccountId: f.account.ID, AssetId: &f.btc.ID})
		require.Len(t, btcOnly.Events, 1)
		assert.Len(t, btcOnly.Events[0].Legs, 2, "the matching event comes with all its legs")

		window := list(owner, &apiv1.ListLedgerRequest{AccountId: f.account.ID, ExecutedFrom: day(2), ExecutedTo: day(3)})
		require.Len(t, window.Events, 1)
		assert.Equal(t, apiv1.LedgerEventKind_LEDGER_EVENT_KIND_TRADE, window.Events[0].Kind)
	})

	t.Run("another user gets the answer foreign holdings give", func(t *testing.T) {
		stranger := middleware.ContextWithUser(f.ctx, createTestUser(t, NewUserStore(testDB.Pool)))
		ledger, ledgerErr := f.handler.ListLedger(stranger, connect.NewRequest(&apiv1.ListLedgerRequest{AccountId: f.account.ID}))
		holdings, holdingsErr := f.handler.ListHoldings(stranger, connect.NewRequest(&apiv1.ListHoldingsRequest{AccountId: &f.account.ID}))

		assert.Equal(t, connect.CodeOf(holdingsErr), connect.CodeOf(ledgerErr))
		require.NoError(t, ledgerErr)
		assert.Empty(t, holdings.Msg.Holdings)
		assert.Empty(t, ledger.Msg.Events)
		assert.Empty(t, ledger.Msg.NextPageToken)
	})
}
