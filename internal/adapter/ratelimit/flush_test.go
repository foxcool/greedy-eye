package ratelimit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/foxcool/greedy-eye/internal/spend"
)

var errStoreDown = errors.New("store down")

// failingUsageStore refuses writes while failing is set, applying none of the
// batch, the way a single failed statement does. during runs inside a refused
// write, standing in for spend that arrives while the flush is in flight.
type failingUsageStore struct {
	*fakeUsageStore

	mu      sync.Mutex
	failing bool
	during  func()
}

func (f *failingUsageStore) setFailing(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = v
}

func (f *failingUsageStore) AddUsage(ctx context.Context, deltas []Usage) error {
	f.mu.Lock()
	failing, during := f.failing, f.during
	if failing {
		// Once per flush in flight, however many writes the flush splits into.
		f.during = nil
	}
	f.mu.Unlock()
	if failing {
		if during != nil {
			during()
		}
		return errStoreDown
	}
	return f.fakeUsageStore.AddUsage(ctx, deltas)
}

// storedRequests sums what the store holds per key, over callers.
func storedRequests(f *fakeUsageStore) map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int64{}
	for _, u := range f.rows {
		out[u.Fingerprint+"/"+u.Caller] += u.Requests
	}
	return out
}

// TestFailedFlushIsRetried: a refused write must not drop the spend it carried.
// Dropping it under-reports the period for good, and the next restart restores
// a counter that lets the plan be spent past its end.
func TestFailedFlushIsRetried(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &failingUsageStore{fakeUsageStore: &fakeUsageStore{}, failing: true}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	a := reg.bucket(Credential{Provider: "test", APIKey: "key-a"})
	b := reg.bucket(Credential{Provider: "test", APIKey: "key-b"})

	for range 3 {
		require.NoError(t, a.reserve(spend.ClassInteractive, "sweep", now))
	}
	require.NoError(t, b.reserve(spend.ClassInteractive, "sweep", now))
	// Spend that lands while the failing write is in flight belongs to the
	// next flush too, once.
	store.during = func() {
		require.NoError(t, a.reserve(spend.ClassInteractive, "sweep", now))
		require.NoError(t, a.reserve(spend.ClassInteractive, "guard", now))
	}

	require.ErrorIs(t, reg.Flush(ctx), errStoreDown)
	store.during = nil
	require.ErrorIs(t, reg.Flush(ctx), errStoreDown, "a second failure must keep the spend too")
	require.Empty(t, storedRequests(store.fakeUsageStore))

	require.NoError(t, b.reserve(spend.ClassInteractive, "sweep", now))
	store.setFailing(false)
	require.NoError(t, reg.Flush(ctx))

	assert.Equal(t, map[string]int64{
		fingerprint("key-a") + "/sweep": 4,
		fingerprint("key-a") + "/guard": 1,
		fingerprint("key-b") + "/sweep": 2,
	}, storedRequests(store.fakeUsageStore))

	// One row per key in one write: a statement that upserts the same row twice
	// is refused by Postgres, so a retry must merge into new spend, not sit
	// beside it.
	require.Len(t, store.added, 1)
	assert.Len(t, store.added[0], 3)

	require.NoError(t, reg.Flush(ctx))
	assert.Len(t, store.added, 1, "a successful flush leaves nothing to retry")
}

// TestFlushFailureIsLogged: the periodic flush has no caller to return to, so a
// failure that is not logged is invisible.
func TestFlushFailureIsLogged(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &failingUsageStore{fakeUsageStore: &fakeUsageStore{}, failing: true}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	var buf bytes.Buffer
	reg.log = slog.New(slog.NewTextHandler(&buf, nil))

	require.NoError(t, reg.bucket(Credential{Provider: "test", APIKey: "key"}).
		reserve(spend.ClassInteractive, spend.Unlabelled, now))
	reg.flushOnce(context.Background())

	assert.Contains(t, buf.String(), "level=ERROR")
	assert.Contains(t, buf.String(), errStoreDown.Error())
}

// TestFlushRetryUnderConcurrentSpend: spend reserved while flushes alternately
// fail and succeed is written exactly once. Run with -race.
func TestFlushRetryUnderConcurrentSpend(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &failingUsageStore{fakeUsageStore: &fakeUsageStore{}}
	reg := quotaRegistry(1_000_000, func() time.Time { return now }, WithUsageStore(store))
	b := reg.bucket(Credential{Provider: "test", APIKey: "key"})

	const workers, perWorker = 4, 2000
	stop := make(chan struct{})
	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			store.setFailing(i%2 == 0)
			_ = reg.Flush(ctx)
		}
	}()
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range perWorker {
				assert.NoError(t, b.reserve(spend.ClassInteractive, "sweep", now))
				runtime.Gosched()
			}
		})
	}
	wg.Wait()
	close(stop)
	<-flushDone

	store.setFailing(false)
	require.NoError(t, reg.Flush(ctx))
	assert.Equal(t, map[string]int64{fingerprint("key") + "/sweep": workers * perWorker},
		storedRequests(store.fakeUsageStore))
}

// poisonedStore rejects, atomically, every statement that carries a row of the
// poisoned key: the way a row the database refuses for good fails its batch.
type poisonedStore struct {
	*fakeUsageStore
	poisoned string
}

func (p *poisonedStore) AddUsage(ctx context.Context, deltas []Usage) error {
	for _, d := range deltas {
		if d.Fingerprint == p.poisoned {
			return errors.New("row rejected")
		}
	}
	return p.fakeUsageStore.AddUsage(ctx, deltas)
}

// TestPoisonedRowDoesNotBlockTheRest: one delta the store can never write must
// not hold the rest of the spend hostage in the statement it keeps failing.
func TestPoisonedRowDoesNotBlockTheRest(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &poisonedStore{fakeUsageStore: &fakeUsageStore{}, poisoned: fingerprint("key-bad")}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	var buf bytes.Buffer
	reg.log = slog.New(slog.NewTextHandler(&buf, nil))
	bad := reg.bucket(Credential{Provider: "test", APIKey: "key-bad"})
	good := reg.bucket(Credential{Provider: "test", APIKey: "key-good"})

	require.NoError(t, bad.reserve(spend.ClassInteractive, "sweep", now))
	require.NoError(t, bad.reserve(spend.ClassInteractive, "sweep", now))
	require.NoError(t, good.reserve(spend.ClassInteractive, "sweep", now))
	require.Error(t, reg.Flush(ctx), "the unwritable row is still reported")
	assert.Equal(t, map[string]int64{fingerprint("key-good") + "/sweep": 1}, storedRequests(store.fakeUsageStore))

	// Later spend rides no failing statement.
	require.NoError(t, good.reserve(spend.ClassInteractive, "sweep", now))
	require.Error(t, reg.Flush(ctx))
	assert.Equal(t, map[string]int64{fingerprint("key-good") + "/sweep": 2}, storedRequests(store.fakeUsageStore))

	// The loss is named, not just counted: provider, key, period, amount.
	log := buf.String()
	assert.Contains(t, log, "level=ERROR")
	assert.Contains(t, log, fingerprint("key-bad"))
	assert.Contains(t, log, "2026-07-01")
	assert.Contains(t, log, "requests=2")
	assert.NotContains(t, log, "key-bad", "the key itself is never logged")

	// Held, not dropped: whoever fixes the row gets the spend.
	store.poisoned = ""
	require.NoError(t, reg.Flush(ctx))
	assert.EqualValues(t, 2, storedRequests(store.fakeUsageStore)[fingerprint("key-bad")+"/sweep"])
}

// TestStoreOutageIsNotBlamedOnRows: when nothing lands, the store is down, not
// the rows, and per-row errors would only multiply the log.
func TestStoreOutageIsNotBlamedOnRows(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &failingUsageStore{fakeUsageStore: &fakeUsageStore{}, failing: true}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	var buf bytes.Buffer
	reg.log = slog.New(slog.NewTextHandler(&buf, nil))
	for _, k := range []string{"a", "b", "c"} {
		require.NoError(t, reg.bucket(Credential{Provider: "test", APIKey: k}).
			reserve(spend.ClassInteractive, "sweep", now))
	}
	require.ErrorIs(t, reg.Flush(context.Background()), errStoreDown)
	assert.Empty(t, buf.String())
}

// flakyStore fails its first n writes, then behaves.
type flakyStore struct {
	*fakeUsageStore
	failures int
}

func (f *flakyStore) AddUsage(ctx context.Context, deltas []Usage) error {
	if f.failures > 0 {
		f.failures--
		return errStoreDown
	}
	return f.fakeUsageStore.AddUsage(ctx, deltas)
}

// TestStopRetriesTheFinalFlush: the process is about to exit, so there is no
// next tick to carry the spend a failed last write held.
func TestStopRetriesTheFinalFlush(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &flakyStore{fakeUsageStore: &fakeUsageStore{}, failures: 2}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	require.NoError(t, reg.Start(context.Background()))
	for range 3 {
		require.NoError(t, reg.bucket(Credential{Provider: "test", APIKey: "key"}).
			reserve(spend.ClassInteractive, "sweep", now))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, reg.Stop(ctx))
	assert.Equal(t, map[string]int64{fingerprint("key") + "/sweep": 3}, storedRequests(store.fakeUsageStore))
}

// TestStopLogsWhatItCouldNotWrite: when the deadline ends the retries, the spend
// that never reached the store is reported once, with its amount.
func TestStopLogsWhatItCouldNotWrite(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &failingUsageStore{fakeUsageStore: &fakeUsageStore{}, failing: true}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	var buf bytes.Buffer
	reg.log = slog.New(slog.NewTextHandler(&buf, nil))
	require.NoError(t, reg.Start(context.Background()))
	b := reg.bucket(Credential{Provider: "test", APIKey: "key"})
	for range 3 {
		require.NoError(t, b.reserve(spend.ClassInteractive, "sweep", now))
	}
	require.NoError(t, b.reserve(spend.ClassInteractive, "guard", now))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, reg.Stop(ctx), errStoreDown)

	log := buf.String()
	assert.Equal(t, 1, strings.Count(log, "lost"), "reported once, not per attempt")
	assert.Contains(t, log, "level=ERROR")
	assert.Contains(t, log, "requests=4")
	assert.Contains(t, log, fingerprint("key"))
}
