//go:build integration

package postgres

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
)

type fixedBrokerSource struct{ syncer entity.BrokerSyncer }

func (s fixedBrokerSource) BrokerSyncerForAccount(*entity.Account) (entity.BrokerSyncer, error) {
	return s.syncer, nil
}

// tinvestReplay serves one cash line, the recorded bond catalogue and the recorded
// operations pages from the adapter's testdata. With failing set, the
// operations method answers 500 and everything else still works.
func tinvestReplay(t *testing.T, failing, renamed *atomic.Bool) *httptest.Server {
	t.Helper()
	page := func(name string) []byte {
		raw, err := os.ReadFile(filepath.Join("..", "..", "adapter", "tinvest", "testdata", name))
		require.NoError(t, err)
		if renamed.Load() {
			raw = []byte(strings.ReplaceAll(string(raw), `"id": "op-`, `"id": "renamed-op-`))
		}
		return raw
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/GetPortfolio"):
			_, _ = w.Write([]byte(`{"positions":[{"figi":"RUB000UTSTOM","instrumentType":"currency","ticker":"RUB000UTSTOM",
				"quantity":{"units":"100","nano":0},"currentPrice":{"currency":"rub","units":"1"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/GetOperationsByCursor"):
			if failing.Load() {
				http.Error(w, `{"message":"internal"}`, http.StatusInternalServerError)
				return
			}
			var req struct {
				Cursor string `json:"cursor"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Cursor == "" {
				_, _ = w.Write(page("operations_page1.json"))
			} else {
				_, _ = w.Write(page("operations_page2.json"))
			}
		case strings.HasSuffix(r.URL.Path, "/Bonds"):
			_, _ = w.Write(page("bonds_shapes.json"))
		default:
			_, _ = w.Write([]byte(`{"instruments":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestTInvestIncome_RecordedOperations: a broker sync reads the recorded
// operations into the ledger, and a second sync writes no row. When the
// operations method then fails, the balances are still written, the cursor
// stays where it was and account health names the failure.
func TestTInvestIncome_RecordedOperations(t *testing.T) {
	pool := getTestPool(t)
	ctx := t.Context()
	s := NewPortfolioStore(pool)
	md := marketdata.NewHandler(NewMarketDataStore(pool), slog.New(slog.DiscardHandler))
	user := createTestUser(t, NewUserStore(pool))
	account, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "T-Invest", Type: entity.AccountTypeBroker,
		Data: map[string]string{"provider": "tinvest", "api_key": "t", "broker_account_id": "acc-1"}})
	require.NoError(t, err)

	var failing, renamed atomic.Bool
	srv := tinvestReplay(t, &failing, &renamed)
	client, err := tinvestadapter.NewClient(tinvestadapter.Config{Token: "t", BaseURL: srv.URL, Transport: http.DefaultTransport})
	require.NoError(t, err)
	h := portfolio.NewHandler(s, slog.New(slog.DiscardHandler)).
		WithMarketDataClient(md).
		WithBrokerSyncerSource(fixedBrokerSource{syncer: tinvestadapter.NewBrokerSyncer(client, "acc-1")})
	uctx := middleware.ContextWithUser(ctx, user)
	sync := func() *apiv1.SyncAccountResponse {
		t.Helper()
		resp, err := h.SyncAccount(uctx, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: account.ID}))
		require.NoError(t, err)
		return resp.Msg
	}
	count := func(query string) int {
		t.Helper()
		var n int
		require.NoError(t, pool.QueryRow(ctx, query, account.ID).Scan(&n))
		return n
	}
	const events = "SELECT count(*) FROM ledger_events WHERE account_id = $1"
	// The recorded operations are older than the re-read window, so a later
	// run would not be sent them at all. Rewinding the cursor makes it read
	// the whole history again, which is what the dedup must survive.
	rewind := func() {
		t.Helper()
		_, err := pool.Exec(ctx, "UPDATE ledger_cursors SET advanced_at = '0001-01-01T00:00:00Z' WHERE account_id = $1", account.ID)
		require.NoError(t, err)
	}

	first := sync()
	require.Empty(t, first.LedgerErrors)
	assert.Equal(t, int32(10), first.LedgerEventsCreated, "two overnight payments of one sum on one day are two")
	assert.Equal(t, 10, count(events))
	assert.Equal(t, 10, count(events+" AND kind = 'income' AND source = 'sync:tinvest'"))
	assert.Equal(t, 1, count("SELECT count(*) FROM holdings WHERE account_id = $1"), "income writes no holding")

	var amount, direction, incomeKind string
	require.NoError(t, pool.QueryRow(ctx, `SELECT l.amount::text, l.direction, e.income_kind FROM ledger_events e
		JOIN ledger_legs l ON l.event_id = e.id WHERE e.account_id = $1 AND e.external_ref = 'op-dividend-tax-1'`, account.ID).
		Scan(&amount, &direction, &incomeKind))
	assert.Equal(t, "67270000000", amount, "the tax to the kopeck at scale 9")
	assert.Equal(t, "out", direction)
	assert.Equal(t, "dividend", incomeKind)

	// A tax that names no income is written as tax, its sign the direction.
	for ref, want := range map[string]string{"op-tax-1": "out", "op-tax-correction-1": "in"} {
		var dir, kind string
		require.NoError(t, pool.QueryRow(ctx, `SELECT l.direction, e.income_kind FROM ledger_events e
			JOIN ledger_legs l ON l.event_id = e.id WHERE e.account_id = $1 AND e.external_ref = $2`, account.ID, ref).
			Scan(&dir, &kind))
		assert.Equal(t, want, dir, ref)
		assert.Equal(t, "tax", kind, ref)
	}

	cursors, err := s.ListLedgerCursors(ctx, account.ID)
	require.NoError(t, err)
	require.Len(t, cursors, 1)
	read := cursors[0]
	assert.Empty(t, read.Chain)
	assert.Equal(t, "sync:tinvest", read.Source)
	assert.Empty(t, read.LastError)
	assert.False(t, read.AdvancedAt.IsZero())

	rewind()
	second := sync()
	require.Empty(t, second.LedgerErrors)
	assert.Zero(t, second.LedgerEventsCreated, "a second run creates 0 rows")
	assert.Equal(t, 10, count(events))

	// The broker says an operation id may change: the same payments under new
	// ids are recognised by what they are, read back from the database.
	renamed.Store(true)
	rewind()
	moved := sync()
	require.Empty(t, moved.LedgerErrors)
	assert.Zero(t, moved.LedgerEventsCreated, "renamed operations create 0 rows")
	assert.Equal(t, 0, count(events+" AND external_ref LIKE 'renamed-%'"))
	renamed.Store(false)

	failing.Store(true)
	time.Sleep(10 * time.Millisecond) // so a moved cursor would show
	cursors, err = s.ListLedgerCursors(ctx, account.ID)
	require.NoError(t, err)
	before := cursors[0].AdvancedAt

	third := sync()
	assert.Empty(t, third.Errors, "the balance sync does not fail")
	assert.Equal(t, int32(1), third.HoldingsUpserted, "balances are written")
	require.Len(t, third.LedgerErrors, 1)
	assert.Contains(t, third.LedgerErrors[0], "500")

	cursors, err = s.ListLedgerCursors(ctx, account.ID)
	require.NoError(t, err)
	assert.True(t, before.Equal(cursors[0].AdvancedAt), "the cursor is unchanged")
	assert.NotEmpty(t, cursors[0].LastError)

	health, err := h.GetAccountHealth(uctx, connect.NewRequest(&apiv1.GetAccountHealthRequest{AccountId: &account.ID}))
	require.NoError(t, err)
	require.Len(t, health.Msg.Accounts, 1)
	var found *apiv1.HealthReason
	for _, r := range health.Msg.Accounts[0].Reasons {
		if r.Kind == apiv1.HealthReasonKind_HEALTH_REASON_KIND_LEDGER_FAILING {
			found = r
		}
	}
	require.NotNil(t, found, "account health shows the operations error")
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_DEGRADED, health.Msg.Accounts[0].State)
	assert.True(t, before.Equal(found.Since.AsTime()), "since is the last full read")

	failing.Store(false)
	recovered := sync()
	require.Empty(t, recovered.LedgerErrors)
	assert.Zero(t, recovered.LedgerEventsCreated)
	cursors, err = s.ListLedgerCursors(ctx, account.ID)
	require.NoError(t, err)
	assert.Empty(t, cursors[0].LastError, "a read clears the failure")
}

// TestTInvestIncome_FirstReadFails: a history never read keeps no read-through
// time, so the next run reads it whole.
func TestTInvestIncome_FirstReadFails(t *testing.T) {
	pool := getTestPool(t)
	ctx := t.Context()
	s := NewPortfolioStore(pool)
	user := createTestUser(t, NewUserStore(pool))
	account, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "T-Invest", Type: entity.AccountTypeBroker})
	require.NoError(t, err)

	require.NoError(t, s.SaveLedgerCursor(ctx, &entity.LedgerCursor{AccountID: account.ID, Source: "sync:tinvest",
		LastError: "status 500", CheckedAt: time.Now()}))
	cursors, err := s.ListLedgerCursors(ctx, account.ID)
	require.NoError(t, err)
	require.Len(t, cursors, 1)
	assert.True(t, cursors[0].AdvancedAt.IsZero(), "never advanced reads back as never")
	assert.Equal(t, "status 500", cursors[0].LastError)
}
