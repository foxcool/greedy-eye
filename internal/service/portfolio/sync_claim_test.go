package portfolio

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// readingSyncer answers each call with the next reading in line. The first
// call parks after its reading is taken, until release closes, so a second
// sync can read the provider later and land first.
type readingSyncer struct {
	mu       sync.Mutex
	readings []string
	calls    int
	parked   chan struct{}
	release  chan struct{}
}

func newReadingSyncer(readings ...string) *readingSyncer {
	return &readingSyncer{readings: readings, parked: make(chan struct{}), release: make(chan struct{})}
}

func (r *readingSyncer) SyncWallet(_ context.Context, _ string, _ []string) ([]entity.WalletBalance, error) {
	r.mu.Lock()
	n := r.calls
	r.calls++
	r.mu.Unlock()

	if n >= len(r.readings) {
		return nil, fmt.Errorf("unexpected provider read #%d", n+1)
	}
	amount := r.readings[n]
	if n == 0 {
		close(r.parked)
		<-r.release
	}
	return []entity.WalletBalance{
		{Symbol: "DAI", Amount: amount, Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
	}, nil
}

func (r *readingSyncer) newest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readings[r.calls-1]
}

// claimFixture is a wallet account whose holdings writes are recorded in
// commit order: the last one is what the account shows.
func claimFixture(t *testing.T, syncer *readingSyncer) (*Handler, *mockStore, func() []decimal.Decimal) {
	t.Helper()
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	var (
		mu      sync.Mutex
		written []decimal.Decimal
	)
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			mu.Lock()
			defer mu.Unlock()
			written = append(written, args.Get(1).(*entity.Holding).Amount)
		}).
		Return(&entity.Holding{ID: "h-dai"}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(syncer)
	return h, s, func() []decimal.Decimal {
		mu.Lock()
		defer mu.Unlock()
		return append([]decimal.Decimal(nil), written...)
	}
}

func syncReq() *connect.Request[apiv1.SyncAccountRequest] {
	return connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID})
}

// TestSyncAccount_OverlappingSyncsKeepTheNewerReading: sync A reads the
// provider, sync B reads it later and commits first, then A commits. Without a
// claim the account ends on A's older reading under a fresh updated_at. The
// second sync is refused before it reads, so the reading that lands is the
// newest one taken.
func TestSyncAccount_OverlappingSyncsKeepTheNewerReading(t *testing.T) {
	syncer := newReadingSyncer("100", "200")
	h, _, written := claimFixture(t, syncer)

	errA := make(chan error, 1)
	go func() {
		_, err := h.SyncAccount(ctxWithUser(testUserID), syncReq())
		errA <- err
	}()
	<-syncer.parked

	_, errB := h.SyncAccount(ctxWithUser(testUserID), syncReq())
	close(syncer.release)
	require.NoError(t, <-errA)

	got := written()
	require.NotEmpty(t, got)
	want := decimal.RequireFromString(syncer.newest())
	assert.True(t, want.Equal(got[len(got)-1]),
		"holdings end on %s, the newest provider reading is %s", got[len(got)-1], want)

	require.Error(t, errB, "the overlapping sync is refused, not run in parallel")
	assert.Equal(t, connect.CodeAborted, connect.CodeOf(errB))
	assert.ErrorIs(t, errB, errSyncInProgress)
}

// TestSyncAccount_ManualSyncDuringSweepIsRefused: the hourly sweep and a person
// pressing Sync land on the same account. The person gets an explicit status;
// the sweep's sync finishes and counts as synced.
func TestSyncAccount_ManualSyncDuringSweepIsRefused(t *testing.T) {
	syncer := newReadingSyncer("100", "200")
	h, s, _ := claimFixture(t, syncer)
	acct, err := s.GetAccount(context.Background(), testAccountID)
	require.NoError(t, err)
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()

	swept := make(chan SweepReport, 1)
	go func() {
		report, err := h.SyncDueAccounts(context.Background(), SweepOpts{})
		assert.NoError(t, err)
		swept <- report
	}()
	<-syncer.parked

	_, err = h.SyncAccount(ctxWithUser(testUserID), syncReq())
	close(syncer.release)
	report := <-swept

	require.Error(t, err)
	assert.Equal(t, connect.CodeAborted, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "already running")
	assert.Equal(t, 1, report.Synced)
	assert.Zero(t, report.Failed)
}

// TestSyncDueAccounts_SkipsAnAccountSyncingElsewhere: the sweep finding an
// account mid-sync is not a failure of the account, and standing it down would
// push back a sync that is about to land.
func TestSyncDueAccounts_SkipsAnAccountSyncingElsewhere(t *testing.T) {
	syncer := newReadingSyncer() // no readings: any provider read is an error
	h, s, written := claimFixture(t, syncer)
	acct, err := s.GetAccount(context.Background(), testAccountID)
	require.NoError(t, err)
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{acct}, nil).Once()
	s.On("RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(1, acct.CreatedAt, nil).Maybe()

	_, err = s.ClaimAccountSync(context.Background(), testAccountID, syncTimeout)
	require.NoError(t, err)

	report, err := h.SyncDueAccounts(context.Background(), SweepOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Picked)
	assert.Zero(t, report.Synced)
	assert.Zero(t, report.Failed)
	assert.Empty(t, report.Notes)
	assert.Empty(t, written())
	s.AssertNotCalled(t, "RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
