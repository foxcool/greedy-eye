package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistry builds a registry whose only provider is paced fast enough
// for a test to observe spacing without spending real seconds.
func newTestRegistry(t *testing.T, rps float64) *Registry {
	t.Helper()
	return NewRegistry(map[string]Limit{"test": {RPS: rps, Burst: 1}})
}

func doRequests(t *testing.T, c *http.Client, url string, n int) {
	t.Helper()
	for range n {
		resp, err := c.Get(url) //nolint:noctx // test helper, context adds nothing
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
}

// TestSharedBudgetAcrossClients is the property the whole package exists for:
// the credentials resolver builds a client per account, and those clients must
// not each get their own budget.
func TestSharedBudgetAcrossClients(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := newTestRegistry(t, 20) // 50ms apart

	// Three separately constructed clients, one key — as a sweep over three
	// accounts on the same provider would produce.
	var wg sync.WaitGroup
	start := time.Now()
	for range 3 {
		client := &http.Client{Transport: reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil)}
		wg.Go(func() {
			doRequests(t, client, srv.URL, 2)
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	assert.EqualValues(t, 6, hits.Load())
	// Six requests at 20/s with burst 1: five gaps of 50ms.
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond,
		"per-client budgets would let these through at once")
}

// TestDistinctKeysDoNotShare: providers meter per key, so two keys must not
// throttle each other.
func TestDistinctKeysDoNotShare(t *testing.T) {
	reg := newTestRegistry(t, 1)

	a := reg.bucket(Credential{Provider: "test", APIKey: "key-a"})
	b := reg.bucket(Credential{Provider: "test", APIKey: "key-b"})
	same := reg.bucket(Credential{Provider: "test", APIKey: "key-a"})

	assert.NotSame(t, a, b)
	assert.Same(t, a, same, "same credential must resolve to one bucket")
}

// TestKeylessTierIsSeparate: the keyless quota is per IP, not per key, and is
// usually tighter — it needs its own bucket and its own limit.
func TestKeylessTierIsSeparate(t *testing.T) {
	reg := NewRegistry(nil)

	assert.NotSame(t, reg.bucket(Credential{Provider: "coingecko"}), reg.bucket(Credential{Provider: "coingecko", APIKey: "key"}))
	assert.Less(t,
		reg.limitFor(Credential{Provider: "coingecko"}).RPS,
		reg.limitFor(Credential{Provider: "coingecko", APIKey: "key"}).RPS,
		"keyless must be the slower tier")
}

func TestLimitResolution(t *testing.T) {
	reg := NewRegistry(map[string]Limit{"subscan": {RPS: 0.5, Burst: 1}})

	assert.Equal(t, 0.5, reg.limitFor(Credential{Provider: "subscan", APIKey: "key"}).RPS, "override wins")
	assert.Equal(t, 0.5, reg.limitFor(Credential{Provider: "subscan"}).RPS, "override covers both tiers")
	assert.Equal(t, defaultLimits["moralis"].RPS, reg.limitFor(Credential{Provider: "moralis", APIKey: "key"}).RPS)
	assert.Equal(t, fallbackLimit.RPS, reg.limitFor(Credential{Provider: "nobody-knows-this-one", APIKey: "key"}).RPS)
}

// TestOverrideDoesNotTouchVolume: an operator throttling their own rate must not
// hand every credential on that provider — including a user's own paid plan —
// an unlimited allowance. Rate is this deployment's business (one IP, metered
// per IP as much as per key); volume is that key's plan and that key's money.
func TestOverrideDoesNotTouchVolume(t *testing.T) {
	reg := NewRegistry(map[string]Limit{"coingecko": {RPS: 0.2, Burst: 1}})

	demo := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key"})
	assert.Equal(t, 0.2, demo.RPS, "the operator's rate applies")
	assert.Equal(t, defaultLimits["coingecko"].Quota, demo.Quota,
		"the plan's monthly allowance survives a rate override")
	assert.Equal(t, QuotaMonth, demo.Period)

	pro := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key", Tier: "pro"})
	assert.Equal(t, 0.2, pro.RPS, "the throttle reaches every credential: the IP is shared")
	assert.Equal(t, defaultLimits["coingecko:pro"].Quota, pro.Quota,
		"a user's paid plan keeps its own allowance")
}

// TestPartialOverrideKeepsTierRate: naming only a burst leaves the tier's rate
// alone, so an operator cannot zero a limit by omission.
func TestPartialOverrideKeepsTierRate(t *testing.T) {
	reg := NewRegistry(map[string]Limit{"coingecko": {Burst: 3}})

	got := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key"})

	assert.Equal(t, defaultLimits["coingecko"].RPS, got.RPS)
	assert.Equal(t, 3, got.Burst)
}

// TestSubscanDefaultUnderPlanCeiling pins the number that caused this package
// to exist: plan #31202 enforces 2 rps and logged us at 3.
func TestSubscanDefaultUnderPlanCeiling(t *testing.T) {
	l := defaultLimits["subscan"]
	assert.Less(t, l.RPS, 2.0)
	assert.Equal(t, 1, l.Burst, "any burst above 1 is what trips a per-second meter")
}

// TestFingerprintDoesNotKeepTheKey: the registry outlives every request and is
// reachable from a panic dump.
func TestFingerprintDoesNotKeepTheKey(t *testing.T) {
	const secret = "super-secret-api-key"

	fp := fingerprint(secret)

	assert.NotContains(t, fp, secret)
	assert.Equal(t, fp, fingerprint(secret), "must be stable")
	assert.Equal(t, "keyless", fingerprint(""))
}

// TestBackoffFreezesBucket: a 429 must stop the next request, and the response
// must still reach the caller intact.
func TestBackoffFreezesBucket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"reason":"rate limit"}`))
	}))
	defer srv.Close()

	reg := newTestRegistry(t, 1000)
	tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil).(*limitedTransport)
	require.True(t, ok)

	now := time.Now()
	tr.now = func() time.Time { return now }

	resp, err := (&http.Client{Transport: tr}).Get(srv.URL) //nolint:noctx // fixed URL
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode,
		"the response belongs to the adapter, not to the limiter")
	assert.Equal(t, 2*time.Minute, tr.bucket.frozenFor(now))
}

// TestFreezeBlocksUntilDeadline: with the deadline passed, traffic resumes on
// its own — no restart, no manual reset.
func TestFreezeBlocksUntilDeadline(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := newTestRegistry(t, 1000)
	tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil).(*limitedTransport)
	require.True(t, ok)

	now := time.Now()
	tr.now = func() time.Time { return now }
	tr.bucket.freezeUntil(now.Add(time.Hour))

	// Context expiry proves the freeze is actually blocking.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	_, err = (&http.Client{Transport: tr}).Do(req)
	require.Error(t, err)
	assert.EqualValues(t, 0, hits.Load(), "request must not reach the provider while frozen")

	// Move past the deadline; the same transport recovers by itself.
	tr.now = func() time.Time { return now.Add(2 * time.Hour) }
	doRequests(t, &http.Client{Transport: tr}, srv.URL, 1)
	assert.EqualValues(t, 1, hits.Load())
}

// TestFreezeNeverShortens: a second, shorter back-off arriving during a long
// one must not release the brake early.
func TestFreezeNeverShortens(t *testing.T) {
	now := time.Now()
	b := newBucket("test", "keyless", Limit{RPS: 1, Burst: 1}, time.Time{})

	b.freezeUntil(now.Add(time.Hour))
	b.freezeUntil(now.Add(time.Minute))

	assert.Equal(t, time.Hour, b.frozenFor(now))
}

func TestRetryAfterParsing(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"seconds", "30", 30 * time.Second},
		{"absent", "", defaultBackoff},
		{"unparsable", "soon", defaultBackoff},
		{"capped", "86400", maxBackoff},
		{"date in the past", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tc.header != "" {
				resp.Header.Set("Retry-After", tc.header)
			}
			assert.Equal(t, tc.want, retryAfter(resp))
		})
	}
}

// TestBlockchairBlacklistCounts: 430 is not a registered status code, but it
// is what Blockchair's free tier sends when it stops answering.
func TestBackoffStatuses(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusTeapot, 430} {
		assert.True(t, backoffStatuses[code], "status %d must trigger back-off", code)
	}
	assert.False(t, backoffStatuses[http.StatusNotFound])
}

// TestNilRegistryIsPassthrough keeps existing tests and any caller without
// budget wiring working.
func TestNilRegistryIsPassthrough(t *testing.T) {
	var reg *Registry
	assert.Nil(t, reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil))
	assert.Equal(t, http.DefaultTransport, reg.Transport(Credential{Provider: "test", APIKey: "key"}, http.DefaultTransport))
}

// fakeUsageStore records what the registry writes and replays what it was
// seeded with, standing in for the database across a simulated restart.
type fakeUsageStore struct {
	mu    sync.Mutex
	rows  []Usage
	added [][]Usage
}

func (f *fakeUsageStore) LoadUsage(_ context.Context, periodStart time.Time) ([]Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Usage
	for _, u := range f.rows {
		if u.PeriodStart.Equal(periodStart) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeUsageStore) AddUsage(_ context.Context, deltas []Usage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, deltas)
	for _, d := range deltas {
		found := false
		for i, u := range f.rows {
			if u.Provider == d.Provider && u.Fingerprint == d.Fingerprint && u.PeriodStart.Equal(d.PeriodStart) {
				f.rows[i].Requests += d.Requests
				f.rows[i].Backoffs += d.Backoffs
				found = true
				break
			}
		}
		if !found {
			f.rows = append(f.rows, d)
		}
	}
	return nil
}

// quotaRegistry builds a registry whose only provider has a small monthly
// allowance, so a test can spend it in a few calls.
func quotaRegistry(quota int, now func() time.Time, opts ...Option) *Registry {
	opts = append([]Option{WithClock(now)}, opts...)
	return NewRegistry(map[string]Limit{
		"test": {RPS: 1000, Burst: 100, Quota: quota, Period: QuotaMonth},
	}, opts...)
}

// TestQuotaClassReserve: background work stops at the reserve so an interactive
// request still has allowance left. This is the difference between a sweep
// eating the month and a person being told "no" on the 20th.
func TestQuotaClassReserve(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(10, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key"}
	b := reg.bucket(cred)

	// backgroundReserve is 0.8, so background gets 8 of 10.
	for i := range 8 {
		require.NoError(t, b.reserve(ClassBackground, now), "background request %d", i)
	}
	require.ErrorIs(t, b.reserve(ClassBackground, now), ErrQuotaExhausted)
	require.NoError(t, b.reserve(ClassInteractive, now), "the reserve is for interactive work")
	require.NoError(t, b.reserve(ClassInteractive, now))
	require.ErrorIs(t, b.reserve(ClassInteractive, now), ErrQuotaExhausted, "hard ceiling for everyone")
}

// TestQuotaPeriodRollover: the allowance resets on the provider's calendar
// boundary, not on a rolling window.
func TestQuotaPeriodRollover(t *testing.T) {
	now := time.Date(2026, 7, 31, 23, 59, 0, 0, time.UTC)
	reg := quotaRegistry(2, func() time.Time { return now })
	b := reg.bucket(Credential{Provider: "test", APIKey: "key"})

	require.NoError(t, b.reserve(ClassInteractive, now))
	require.NoError(t, b.reserve(ClassInteractive, now))
	require.ErrorIs(t, b.reserve(ClassInteractive, now), ErrQuotaExhausted)

	next := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, b.reserve(ClassInteractive, next), "a new month is a new allowance")
}

// TestRemainingSizesTheSweep: the portion a sweep may spend comes from what is
// left of the plan, so changing the plan changes the budget with it.
func TestRemainingSizesTheSweep(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(1000, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key"}

	left, end, ok := reg.Remaining(cred)
	require.True(t, ok)
	assert.Equal(t, 800, left, "background may spend the reserve share")
	assert.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), end)

	require.NoError(t, reg.bucket(cred).reserve(ClassBackground, now))
	left, _, _ = reg.Remaining(cred)
	assert.Equal(t, 799, left)

	// A rate-only plan has nothing to divide.
	_, _, ok = reg.Remaining(Credential{Provider: "esplora"})
	assert.False(t, ok)
}

// TestUsagePersistsAcrossRestart: a monthly allowance tracked only in memory is
// no allowance at all — a deploy would hand the process a fresh one.
func TestUsagePersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := &fakeUsageStore{}
	cred := Credential{Provider: "test", APIKey: "key"}

	reg := quotaRegistry(10, clock, WithUsageStore(store))
	require.NoError(t, reg.Start(ctx))
	b := reg.bucket(cred)
	for range 6 {
		require.NoError(t, b.reserve(ClassInteractive, now))
	}
	require.NoError(t, reg.Stop(ctx))

	restarted := quotaRegistry(10, clock, WithUsageStore(store))
	require.NoError(t, restarted.Start(ctx))
	got := restarted.bucket(cred).snapshot()
	assert.EqualValues(t, 6, got.Requests, "spend must survive the restart")
	assert.Equal(t, fingerprint("key"), got.Fingerprint, "the key itself is never stored")
	assert.NotContains(t, got.Fingerprint, "key")

	// Restored spend counts against the background reserve (8 of 10): two more
	// background requests fit, the third does not.
	require.NoError(t, restarted.bucket(cred).reserve(ClassBackground, now))
	require.NoError(t, restarted.bucket(cred).reserve(ClassBackground, now))
	require.ErrorIs(t, restarted.bucket(cred).reserve(ClassBackground, now), ErrQuotaExhausted)
}

// TestFlushSendsOnlyDeltas: counters are added to, not set, so two backend
// instances sum the way the provider sums them.
func TestFlushSendsOnlyDeltas(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	b := reg.bucket(Credential{Provider: "test", APIKey: "key"})

	require.NoError(t, b.reserve(ClassInteractive, now))
	require.NoError(t, reg.Flush(ctx))
	require.NoError(t, b.reserve(ClassInteractive, now))
	require.NoError(t, reg.Flush(ctx))
	require.NoError(t, reg.Flush(ctx), "nothing pending is not a write")

	require.Len(t, store.added, 2)
	assert.EqualValues(t, 1, store.added[0][0].Requests)
	assert.EqualValues(t, 1, store.added[1][0].Requests)
	assert.EqualValues(t, 2, store.rows[0].Requests)
}

// TestTierSelectsLimit: the plan named on the account picks the limit, and an
// unnamed one is inferred from whether a key is present at all.
func TestTierSelectsLimit(t *testing.T) {
	reg := NewRegistry(nil)

	keyless := reg.limitFor(Credential{Provider: "coingecko"})
	demo := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key"})
	pro := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key", Tier: "pro"})

	assert.Zero(t, keyless.Quota, "the keyless tier meters rate, not volume")
	assert.Equal(t, 10000, demo.Quota, "free keyed plan is the demo allowance")
	assert.Equal(t, QuotaMonth, demo.Period)
	assert.Greater(t, pro.Quota, demo.Quota)

	assert.NotSame(t,
		reg.bucket(Credential{Provider: "coingecko", APIKey: "key"}),
		reg.bucket(Credential{Provider: "coingecko", APIKey: "key", Tier: "pro"}),
		"a tier change is a different budget")
}

// TestQuotaRefusalReachesCaller: an exhausted quota surfaces as an error rather
// than a request, because no amount of waiting fixes it before the period ends.
func TestQuotaRefusalReachesCaller(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(1, func() time.Time { return now })
	client := &http.Client{Transport: reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil)}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	req2, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req2) //nolint:bodyclose // the request never goes out
	require.ErrorIs(t, err, ErrQuotaExhausted)
	assert.EqualValues(t, 1, hits.Load(), "the refused request must not reach the provider")
}

// TestBackoffsAreCounted: a rising back-off count is what tells an operator the
// rate limit, not the volume limit, needs attention.
func TestBackoffsAreCounted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	reg := newTestRegistry(t, 100)
	cred := Credential{Provider: "test", APIKey: "key"}
	client := &http.Client{Transport: reg.Transport(cred, nil)}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	snap := reg.Snapshot()
	require.Len(t, snap, 1)
	assert.EqualValues(t, 1, snap[0].Requests)
	assert.EqualValues(t, 1, snap[0].Backoffs)
}
