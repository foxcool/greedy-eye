//go:build integration

package postgres

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	tinvestadapter "github.com/foxcool/greedy-eye/internal/adapter/tinvest"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FIGIs the recorded operations name (adapter testdata).
const (
	shareFIGI    = "BBG004730N88" // op-dividend-1 and its tax
	bondFIGI     = "BBG00BONDXX"  // op-coupon-1 and its tax
	usdShareFIGI = "BBG000BPH459" // op-usd-dividend-1
)

// TestIncomePayer_RecordedOperations: a dividend operation naming a known
// FIGI is written with that share as its payer, and a report filtered by the
// share returns only its rows. A paper the catalogue does not hold leaves the
// event without a payer and is named, not dropped. The backfill sets the
// payer on events written before payers existed, a second run sets nothing,
// and the report's total is the same however its rows are split.
func TestIncomePayer_RecordedOperations(t *testing.T) {
	pool := getTestPool(t)
	ctx := t.Context()
	s := NewPortfolioStore(pool)
	catalogue := NewMarketDataStore(pool)
	md := marketdata.NewHandler(catalogue, slog.New(slog.DiscardHandler))
	user := createTestUser(t, NewUserStore(pool))
	admin := createTestUser(t, NewUserStore(pool))
	admin.Roles = []string{"admin"}
	account, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "T-Invest", Type: entity.AccountTypeBroker,
		Data: map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "acc-1"}})
	require.NoError(t, err)

	bind := func(symbol, figi string) *entity.Asset {
		t.Helper()
		a, err := catalogue.CreateAsset(ctx, &entity.Asset{Symbol: symbol, Name: symbol, Type: entity.AssetTypeStock, Market: "moex"})
		require.NoError(t, err)
		_, err = catalogue.CreateAssetExternalRef(ctx, &entity.AssetExternalRef{AssetID: a.ID, Source: tinvestadapter.ProviderName, Ref: figi})
		require.NoError(t, err)
		return a
	}
	share := bind("PAYSBER", shareFIGI)

	var failing, renamed atomic.Bool
	srv := tinvestReplay(t, &failing, &renamed)
	client, err := tinvestadapter.NewClient(tinvestadapter.Config{Token: "t", BaseURL: srv.URL, Transport: http.DefaultTransport})
	require.NoError(t, err)
	h := portfolio.NewHandler(s, slog.New(slog.DiscardHandler)).
		WithMarketDataClient(md).
		WithBrokerSyncerSource(fixedBrokerSource{syncer: tinvestadapter.NewBrokerSyncer(client, "acc-1")}).
		WithIncomePayerRefs(map[string]portfolio.IncomePayerRefFunc{tinvestadapter.ProviderName: tinvestadapter.IncomePayerRef})
	uctx := middleware.ContextWithUser(ctx, user)
	actx := middleware.ContextWithUser(ctx, admin)

	synced, err := h.SyncAccount(uctx, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: account.ID}))
	require.NoError(t, err)
	require.Empty(t, synced.Msg.LedgerErrors)
	require.Equal(t, int32(10), synced.Msg.LedgerEventsCreated)

	payerOf := func(ref string) string {
		t.Helper()
		var payer *string
		require.NoError(t, pool.QueryRow(ctx, `SELECT payer_asset_id::text FROM ledger_events
			WHERE account_id = $1 AND external_ref = $2`, account.ID, ref).Scan(&payer))
		if payer == nil {
			return ""
		}
		return *payer
	}
	assert.Equal(t, share.ID, payerOf("op-dividend-1"), "the dividend names the share that paid it")
	assert.Equal(t, share.ID, payerOf("op-dividend-tax-1"), "and so does the tax withheld on it")
	assert.Empty(t, payerOf("op-coupon-1"), "a bond the catalogue does not hold is not guessed")
	assert.Empty(t, payerOf("op-overnight-1"), "no paper paid overnight interest")
	assert.Empty(t, payerOf("op-tax-1"), "a tax that names no income has no payer")

	var rubID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT l.asset_id::text FROM ledger_events e JOIN ledger_legs l ON l.event_id = e.id
		WHERE e.account_id = $1 AND e.external_ref = 'op-dividend-1'`, account.ID).Scan(&rubID))
	report := func(assetID *string) *apiv1.GetIncomeReportResponse {
		t.Helper()
		resp, err := h.GetIncomeReport(uctx, connect.NewRequest(&apiv1.GetIncomeReportRequest{
			AccountId:    &account.ID,
			From:         timestamppb.New(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)),
			To:           timestamppb.New(time.Now().Add(time.Hour)),
			QuoteAssetId: &rubID,
			AssetId:      assetID,
		}))
		require.NoError(t, err)
		return resp.Msg
	}

	whole := report(nil)
	var shareRows int
	for _, r := range whole.Rows {
		if r.PayerAssetId == share.ID {
			shareRows++
			assert.Equal(t, "PAYSBER", r.PayerSymbol)
			assert.Equal(t, apiv1.IncomeKind_INCOME_KIND_DIVIDEND, r.IncomeKind)
			assert.Equal(t, "RUB", r.Symbol, "symbol stays the currency paid in")
		}
	}
	assert.Equal(t, 1, shareRows, "the dividend and its tax are one row")
	unresolved := func(r []*apiv1.UnresolvedPayer) map[string]int32 {
		out := map[string]int32{}
		for _, u := range r {
			assert.Equal(t, tinvestadapter.ProviderName, u.RefSource)
			out[u.Ref] = u.Events
		}
		return out
	}
	assert.Equal(t, map[string]int32{bondFIGI: 2, usdShareFIGI: 1}, unresolved(whole.UnresolvedPayers),
		"papers the catalogue does not hold are counted and named")

	filtered := report(&share.ID)
	require.Len(t, filtered.Rows, 1, "filtered by the share, only its rows")
	assert.Equal(t, share.ID, filtered.Rows[0].PayerAssetId)
	assert.Empty(t, filtered.UnresolvedPayers)

	// Events written before payers existed: none names one. The bond arrives
	// in the catalogue later, as a balance sync would bring it.
	_, err = pool.Exec(ctx, "UPDATE ledger_events SET payer_asset_id = NULL WHERE account_id = $1", account.ID)
	require.NoError(t, err)
	legacy := report(nil)
	bond := bind("PAYBOND", bondFIGI)

	_, err = h.BackfillIncomePayers(uctx, connect.NewRequest(&apiv1.BackfillIncomePayersRequest{}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a user cannot run the backfill")

	first, err := h.BackfillIncomePayers(actx, connect.NewRequest(&apiv1.BackfillIncomePayersRequest{}))
	require.NoError(t, err)
	assert.Equal(t, int32(4), first.Msg.PayersSet, "two share events, two bond events")
	assert.Equal(t, map[string]int32{usdShareFIGI: 1}, unresolved(first.Msg.UnresolvedPayers))
	assert.Equal(t, share.ID, payerOf("op-dividend-tax-1"))
	assert.Equal(t, bond.ID, payerOf("op-coupon-1"))
	assert.Empty(t, payerOf("op-tax-1"))

	var updatedAt time.Time
	require.NoError(t, pool.QueryRow(ctx, "SELECT max(updated_at) FROM ledger_events WHERE account_id = $1", account.ID).Scan(&updatedAt))
	second, err := h.BackfillIncomePayers(actx, connect.NewRequest(&apiv1.BackfillIncomePayersRequest{}))
	require.NoError(t, err)
	assert.Zero(t, second.Msg.PayersSet, "a second run sets nothing")
	assert.Equal(t, first.Msg.UnresolvedPayers, second.Msg.UnresolvedPayers)
	var updatedAgain time.Time
	require.NoError(t, pool.QueryRow(ctx, "SELECT max(updated_at) FROM ledger_events WHERE account_id = $1", account.ID).Scan(&updatedAgain))
	assert.True(t, updatedAt.Equal(updatedAgain), "and touches no row")

	after := report(nil)
	payers := func(r *apiv1.GetIncomeReportResponse) map[string]bool {
		out := map[string]bool{}
		for _, row := range r.Rows {
			out[row.PayerSymbol] = true
		}
		return out
	}
	assert.Equal(t, map[string]bool{"": true}, payers(legacy), "before the backfill no row names a payer")
	assert.Equal(t, map[string]bool{"": true, "PAYSBER": true, "PAYBOND": true}, payers(after))
	assert.Equal(t, legacy.NetValue, after.NetValue, "the total does not move")
	assert.Equal(t, legacy.GrossValue, after.GrossValue)
	assert.Equal(t, legacy.TaxValue, after.TaxValue)

	// A merge moves the payer with the rest of what pointed at the duplicate.
	dup, err := catalogue.CreateAsset(ctx, &entity.Asset{Symbol: "PAYSBERDUP", Name: "dup", Type: entity.AssetTypeStock, Market: "moex"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE ledger_events SET payer_asset_id = $2 WHERE payer_asset_id = $1", share.ID, dup.ID)
	require.NoError(t, err)
	merged, err := catalogue.MergeAssets(ctx, mergeOpts(dup, share, false))
	require.NoError(t, err)
	assert.Equal(t, int64(2), merged.LedgerPayersMoved)
	assert.Equal(t, share.ID, payerOf("op-dividend-1"))

	// The schema refuses a payer on a tax that names no income.
	_, err = pool.Exec(ctx, "UPDATE ledger_events SET payer_asset_id = $2 WHERE account_id = $1 AND external_ref = 'op-tax-1'", account.ID, share.ID)
	require.Error(t, err)
}
