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
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// raceTimeout bounds every wait in these tests, so a lock that never frees
// fails the test instead of hanging the suite.
const raceTimeout = 15 * time.Second

// lockPoll is how often waitOnLockOrDone looks at pg_stat_activity.
const lockPoll = 10 * time.Millisecond

// parkingSyncer reports one balance. When parked is set it signals there,
// after the sync has already read the account, and waits for release.
type parkingSyncer struct {
	parked, release chan struct{}
}

func (p *parkingSyncer) SyncWallet(context.Context, string, []string) ([]entity.WalletBalance, error) {
	if p.parked != nil {
		close(p.parked)
		<-p.release
	}
	return []entity.WalletBalance{
		{Symbol: "MOVE", Amount: "1", Decimals: 2, ContractAddress: "0xmove", Chain: "eth"},
	}, nil
}

// cashMD resolves every balance to the fixture's cash asset and fetches no
// prices: the race is about where a holding lands, not what it is worth.
// parked/release park GetAsset, which import calls after reading the account.
type cashMD struct {
	portfolio.MarketDataClient
	cash            *entity.Asset
	parked, release chan struct{}
}

func (m *cashMD) FindOrCreateAsset(context.Context, *connect.Request[apiv1.FindOrCreateAssetRequest]) (*connect.Response[apiv1.FindOrCreateAssetResponse], error) {
	return connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: m.cash.ID}}), nil
}

func (m *cashMD) FetchExternalPrices(context.Context, *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error) {
	return connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil
}

func (m *cashMD) GetAsset(ctx context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if m.parked != nil {
		close(m.parked)
		<-m.release
	}
	return m.MarketDataClient.GetAsset(ctx, req)
}

// pausingStore holds the sync's snapshot transaction open: after every write
// in it ran (written/commit), or right after it locked the account
// (locked/resume). Nil channels skip that pause.
type pausingStore struct {
	*PortfolioStore
	written, commit chan struct{}
	locked, resume  chan struct{}
}

func (s *pausingStore) InHoldingsTx(ctx context.Context, fn func(portfolio.HoldingWriter) error) error {
	return s.PortfolioStore.InHoldingsTx(ctx, func(w portfolio.HoldingWriter) error {
		if err := fn(lockPausingWriter{HoldingWriter: w, s: s}); err != nil {
			return err
		}
		if s.written != nil {
			close(s.written)
			<-s.commit
		}
		return nil
	})
}

type lockPausingWriter struct {
	portfolio.HoldingWriter
	s *pausingStore
}

func (w lockPausingWriter) LockAccountPortfolio(ctx context.Context, accountID string) (string, error) {
	id, err := w.HoldingWriter.LockAccountPortfolio(ctx, accountID)
	if w.s.locked != nil {
		close(w.s.locked)
		<-w.s.resume
	}
	return id, err
}

func newRaceAccount(t *testing.T, f *moveFixture, typ entity.AccountType) *entity.Account {
	t.Helper()
	a := &entity.Account{UserID: f.user.ID, Name: "racer", Type: typ, PortfolioID: f.a.ID}
	if typ == entity.AccountTypeWallet {
		a.Data = map[string]string{"address": "0xabc", "chain": "eth"}
		a.Capabilities = []entity.AccountCapability{entity.CapabilityPortfolioSync}
	}
	acct, err := f.store.CreateAccount(context.Background(), a)
	require.NoError(t, err)
	return acct
}

func (f *moveFixture) cashMD() *cashMD {
	return &cashMD{MarketDataClient: marketdata.NewHandler(NewMarketDataStore(testDB.Pool), slog.New(slog.DiscardHandler)), cash: f.cash}
}

// waitOnLockOrDone returns once done is closed or some backend of the test
// database waits on a lock. Either is the state the test needs before letting
// the other side go: the old code never waits, the fixed code always does.
func waitOnLockOrDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(raceTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		default:
		}
		var waiting int
		require.NoError(t, testDB.Pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting))
		if waiting > 0 {
			return
		}
		time.Sleep(lockPoll)
	}
	t.Fatal("neither finished nor waited on a lock")
}

func requireStillRunning(t *testing.T, done <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-done:
		t.Fatal(msg)
	default:
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(raceTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// goResult runs fn in a goroutine. done is for select; result blocks until
// fn returned and gives its error.
func goResult(fn func() error) (done <-chan struct{}, result func() error) {
	ch := make(chan struct{})
	var err error
	go func() {
		defer close(ch)
		err = fn()
	}()
	return ch, func() error { <-ch; return err }
}

func (f *moveFixture) moveAsync(acct *entity.Account, s *PortfolioStore) (<-chan struct{}, func() error) {
	return goResult(func() error {
		_, err := s.UpdateAccount(context.Background(),
			&entity.Account{ID: acct.ID, PortfolioID: f.b.ID}, []string{"portfolio_id"})
		return err
	})
}

func (f *moveFixture) onlyHolding(t *testing.T, acct *entity.Account) *entity.Holding {
	t.Helper()
	hs, _, err := f.store.ListHoldings(context.Background(), portfolio.ListHoldingsOpts{AccountID: acct.ID})
	require.NoError(t, err)
	require.Len(t, hs, 1)
	return hs[0]
}

// TestSyncAfterAMoveLandsInTheNewPortfolio: the sync read the account while it
// was in a, the move then writes and holds its lock, and the sync writes. The
// sync's new holding must land in b — the portfolio the account is in once
// both committed — not in the a it read before the move.
func TestSyncAfterAMoveLandsInTheNewPortfolio(t *testing.T) {
	f := newMoveFixture(t)
	acct := newRaceAccount(t, f, entity.AccountTypeWallet)

	syncer := &parkingSyncer{parked: make(chan struct{}), release: make(chan struct{})}
	h := portfolio.NewHandler(f.store, slog.New(slog.DiscardHandler)).
		WithMarketDataClient(f.cashMD()).WithWalletSyncer(syncer)

	moved, commit := make(chan struct{}), make(chan struct{})
	f.store.moveWritten = func() { close(moved); <-commit }

	syncDone := make(chan struct{})
	var syncErr error
	go func() {
		defer close(syncDone)
		ctx, cancel := context.WithTimeout(middleware.ContextWithUser(context.Background(), f.user), raceTimeout)
		defer cancel()
		_, syncErr = h.SyncAccount(ctx, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID}))
	}()
	waitFor(t, syncer.parked, "the sync to read the account")

	moveDone, moveErr := f.moveAsync(acct, f.store)
	waitFor(t, moved, "the move to write")

	close(syncer.release)
	waitOnLockOrDone(t, syncDone)
	close(commit)
	waitFor(t, moveDone, "the move")
	waitFor(t, syncDone, "the sync")
	require.NoError(t, moveErr())
	require.NoError(t, syncErr)

	assert.Equal(t, f.b.ID, f.onlyHolding(t, acct).PortfolioID, "the sync's holding follows the account it wrote for")
}

// TestMoveDuringASyncCarriesTheSyncsHoldings: the sync's snapshot is written
// and not yet committed when the move runs. The move must wait for it and then
// carry its rows, rather than move what it can see and leave the sync's
// holdings in a once they commit.
func TestMoveDuringASyncCarriesTheSyncsHoldings(t *testing.T) {
	f := newMoveFixture(t)
	acct := newRaceAccount(t, f, entity.AccountTypeWallet)

	ps := &pausingStore{PortfolioStore: f.store, written: make(chan struct{}), commit: make(chan struct{})}
	h := portfolio.NewHandler(ps, slog.New(slog.DiscardHandler)).
		WithMarketDataClient(f.cashMD()).WithWalletSyncer(&parkingSyncer{})

	syncDone := make(chan struct{})
	var syncErr error
	go func() {
		defer close(syncDone)
		ctx, cancel := context.WithTimeout(middleware.ContextWithUser(context.Background(), f.user), raceTimeout)
		defer cancel()
		_, syncErr = h.SyncAccount(ctx, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID}))
	}()
	waitFor(t, ps.written, "the sync to write its snapshot")

	moveDone, moveErr := f.moveAsync(acct, f.store)
	waitOnLockOrDone(t, moveDone)
	requireStillRunning(t, moveDone, "the move waits for the sync's account lock")
	close(ps.commit)
	waitFor(t, syncDone, "the sync")
	waitFor(t, moveDone, "the move")
	require.NoError(t, syncErr)
	require.NoError(t, moveErr())

	assert.Equal(t, f.b.ID, f.onlyHolding(t, acct).PortfolioID, "the move carried the sync's holding")
}

// TestImportAfterAMoveLandsInTheNewPortfolio is the sync race on the import
// path: the import read the account in a, and writes after the move.
func TestImportAfterAMoveLandsInTheNewPortfolio(t *testing.T) {
	f := newMoveFixture(t)
	acct := newRaceAccount(t, f, entity.AccountTypeManual)

	md := f.cashMD()
	md.parked, md.release = make(chan struct{}), make(chan struct{})
	h := portfolio.NewHandler(f.store, slog.New(slog.DiscardHandler)).WithMarketDataClient(md)

	moved, commit := make(chan struct{}), make(chan struct{})
	f.store.moveWritten = func() { close(moved); <-commit }

	importDone := make(chan struct{})
	var (
		resp      *connect.Response[apiv1.ImportPositionsResponse]
		importErr error
	)
	go func() {
		defer close(importDone)
		ctx, cancel := context.WithTimeout(middleware.ContextWithUser(context.Background(), f.user), raceTimeout)
		defer cancel()
		resp, importErr = h.ImportPositions(ctx, connect.NewRequest(&apiv1.ImportPositionsRequest{
			AccountId: acct.ID,
			Positions: []*apiv1.ImportPositionItem{{Symbol: "MOVE", Amount: "1", AssetId: &f.cash.ID}},
		}))
	}()
	waitFor(t, md.parked, "the import to read the account")

	moveDone, moveErr := f.moveAsync(acct, f.store)
	waitFor(t, moved, "the move to write")

	close(md.release)
	waitOnLockOrDone(t, importDone)
	close(commit)
	waitFor(t, moveDone, "the move")
	waitFor(t, importDone, "the import")
	require.NoError(t, moveErr())
	require.NoError(t, importErr)
	require.Equal(t, int32(1), resp.Msg.Created, "%v", resp.Msg.Items)

	assert.Equal(t, f.b.ID, f.onlyHolding(t, acct).PortfolioID, "the imported holding follows the account")
}

// TestUpdateAccountDuplicateBrokerIdentityOnOnePoolConnection: the refused
// update names the account holding the broker identity. That lookup must not
// need a second connection while the aborted transaction still holds the
// first, or N such updates on a pool of N wait on each other for good.
func TestUpdateAccountDuplicateBrokerIdentityOnOnePoolConnection(t *testing.T) {
	getTestPool(t)
	cfg := testDB.Pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	s := NewPortfolioStore(pool)
	user := createTestUser(t, NewUserStore(pool))
	broker := func(id string) *entity.Account {
		return &entity.Account{
			UserID: user.ID, Name: "b", Type: entity.AccountTypeBroker,
			Data: map[string]string{"provider": "tinvest", "broker_account_id": id},
		}
	}
	holder, err := s.CreateAccount(context.Background(), broker("777"))
	require.NoError(t, err)
	other, err := s.CreateAccount(context.Background(), broker("888"))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), raceTimeout)
	defer cancel()
	_, err = s.UpdateAccount(ctx, &entity.Account{
		ID: other.ID, Data: map[string]string{"provider": "tinvest", "broker_account_id": "777"},
	}, []string{"data"})
	require.ErrorIs(t, err, store.ErrAlreadyExists)
	assert.NoError(t, ctx.Err(), "answered without waiting for a connection")
	assert.Contains(t, err.Error(), holder.ID, "the lookup ran, on the connection the transaction gave back")
}

func TestLockAccountPortfolio(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()
	unplaced, err := f.store.CreateAccount(ctx, &entity.Account{UserID: f.user.ID, Name: "unplaced", Type: entity.AccountTypeManual})
	require.NoError(t, err)

	for _, tc := range []struct {
		name, accountID, want string
		err                   error
	}{
		{"in a portfolio", f.account.ID, f.a.ID, nil},
		{"in none", unplaced.ID, "", nil},
		{"gone", uuid.NewString(), "", store.ErrNotFound}, // a well-formed id no row has
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			err := f.store.InHoldingsTx(ctx, func(w portfolio.HoldingWriter) error {
				var err error
				got, err = w.LockAccountPortfolio(ctx, tc.accountID)
				return err
			})
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDeleteDuringASyncDoesNotDeadlock: the sync has locked the account and
// is about to update the holding it wrote before; the owner deletes the
// account. Both must finish — the delete after the sync — instead of each
// waiting on a lock the other holds until Postgres aborts one of them.
func TestDeleteDuringASyncDoesNotDeadlock(t *testing.T) {
	f := newMoveFixture(t)
	acct := newRaceAccount(t, f, entity.AccountTypeWallet)
	ctx := middleware.ContextWithUser(context.Background(), f.user)
	md := f.cashMD()

	// The first sync writes the holding the second one will update.
	_, err := portfolio.NewHandler(f.store, slog.New(slog.DiscardHandler)).
		WithMarketDataClient(md).WithWalletSyncer(&parkingSyncer{}).
		SyncAccount(ctx, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID}))
	require.NoError(t, err)
	f.onlyHolding(t, acct)

	ps := &pausingStore{PortfolioStore: f.store, locked: make(chan struct{}), resume: make(chan struct{})}
	h := portfolio.NewHandler(ps, slog.New(slog.DiscardHandler)).
		WithMarketDataClient(md).WithWalletSyncer(&parkingSyncer{})
	syncDone, syncErr := goResult(func() error {
		ctx, cancel := context.WithTimeout(ctx, raceTimeout)
		defer cancel()
		_, err := h.SyncAccount(ctx, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID}))
		return err
	})
	waitFor(t, ps.locked, "the sync to lock the account")

	deleteDone, deleteErr := goResult(func() error {
		return f.store.DeleteAccountWithHoldings(context.Background(), acct.ID)
	})
	waitOnLockOrDone(t, deleteDone)
	requireStillRunning(t, deleteDone, "the delete waits for the sync's account lock")
	close(ps.resume)
	waitFor(t, syncDone, "the sync")
	waitFor(t, deleteDone, "the delete")
	require.NoError(t, syncErr())
	require.NoError(t, deleteErr())

	_, err = f.store.GetAccount(context.Background(), acct.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}
