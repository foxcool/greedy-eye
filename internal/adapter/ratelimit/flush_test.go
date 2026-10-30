package ratelimit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
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

// TestFlushRetryUnderConcurrentSpend: spend reserved while flushes fail and
// succeed at random is written exactly once. Run with -race.
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
