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

	"github.com/foxcool/greedy-eye/internal/spend"
)

// newTestRegistry builds a registry whose only provider is paced fast enough
// for a test to observe spacing without spending real seconds.
func newTestRegistry(t *testing.T, rps float64) *Registry {
	t.Helper()
	setBuiltinLimit(t, "test", Limit{RPS: rps, Burst: 1})
	return NewRegistry()
}

// setBuiltinLimit gives a fake provider a limit through the built-in table,
// the way a real provider gets its own, and removes it when the test ends.
// Tests using it must not run in parallel.
func setBuiltinLimit(t *testing.T, provider string, l Limit) {
	t.Helper()
	prev, had := defaultLimits[provider]
	defaultLimits[provider] = l
	t.Cleanup(func() {
		if had {
			defaultLimits[provider] = prev
		} else {
			delete(defaultLimits, provider)
		}
	})
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
	reg := NewRegistry()

	assert.NotSame(t, reg.bucket(Credential{Provider: "coingecko"}), reg.bucket(Credential{Provider: "coingecko", APIKey: "key"}))
	assert.Less(t,
		reg.limitFor(Credential{Provider: "coingecko"}).RPS,
		reg.limitFor(Credential{Provider: "coingecko", APIKey: "key"}).RPS,
		"keyless must be the slower tier")
}

// TestKeylessCoinGeckoStaysUnderThePublishedCeiling: the keyless tier is the
// default source on a fresh instance and its quota is per IP, so pacing at the
// ceiling itself leaves no room for any other client behind the same address.
func TestKeylessCoinGeckoStaysUnderThePublishedCeiling(t *testing.T) {
	const publishedPerMinute = 30

	l := NewRegistry().limitFor(Credential{Provider: "coingecko"})

	assert.Less(t, l.RPS*60, float64(publishedPerMinute))
	assert.Equal(t, 1, l.Burst, "a burst is what trips a per-minute meter")
}

func TestLimitResolution(t *testing.T) {
	reg := NewRegistry()

	assert.Equal(t, defaultLimits["subscan"].RPS, reg.limitFor(Credential{Provider: "subscan", APIKey: "key"}).RPS)
	assert.Equal(t, defaultLimits["moralis"].RPS, reg.limitFor(Credential{Provider: "moralis", APIKey: "key"}).RPS)
	assert.Equal(t, fallbackLimit.RPS, reg.limitFor(Credential{Provider: "nobody-knows-this-one", APIKey: "key"}).RPS)
}

// TestCredentialRateDoesNotTouchVolume: an account setting only its own rate
// keeps the plan's allowance; naming a rate must not disable volume accounting.
func TestCredentialRateDoesNotTouchVolume(t *testing.T) {
	reg := NewRegistry()
	own := Limit{RPS: 0.2, Burst: 1}

	demo := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key", Limit: own})
	assert.Equal(t, 0.2, demo.RPS)
	assert.Equal(t, defaultLimits["coingecko"].Quota, demo.Quota,
		"the plan's monthly allowance survives a rate-only Limit")
	assert.Equal(t, QuotaMonth, demo.Period)

	pro := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key", Tier: "pro", Limit: own})
	assert.Equal(t, 0.2, pro.RPS)
	assert.Equal(t, defaultLimits["coingecko:pro"].Quota, pro.Quota)
}

// TestPartialCredentialLimitKeepsTierRate: naming only a burst leaves the
// tier's rate alone, so a Limit cannot zero a field by omission.
func TestPartialCredentialLimitKeepsTierRate(t *testing.T) {
	reg := NewRegistry()

	got := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key", Limit: Limit{Burst: 3}})

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

// TestSubscanVolumeIsMetered: from 2026-08-20 the free plan caps volume and
// bills refused calls. Without a quota the spend is filed under a period that
// never rolls over, so the instance cannot say how much of the month it used.
func TestSubscanVolumeIsMetered(t *testing.T) {
	l := defaultLimits["subscan"]
	assert.Equal(t, QuotaMonth, l.Period)
	assert.LessOrEqual(t, l.Quota, 500000, "the free plan's monthly ceiling")
	assert.Positive(t, l.Quota)
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

	// A cancel with no deadline proves the freeze is actually blocking: a
	// deadline inside the freeze would be refused at once (ErrFrozen).
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	start := time.Now()
	_, err = (&http.Client{Transport: tr}).Do(req)
	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond, "waited in the freeze until cancelled")
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
	b := newBucket("test", "keyless", "", Limit{RPS: 1, Burst: 1}, time.Time{})

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
			assert.Equal(t, tc.want, retryAfter(resp, defaultBackoff))
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
	// Summed over callers, like the real store: restore reads one total per key.
	var out []Usage
	for _, u := range f.rows {
		if !u.PeriodStart.Equal(periodStart) {
			continue
		}
		merged := false
		for i := range out {
			if out[i].Provider == u.Provider && out[i].Fingerprint == u.Fingerprint {
				out[i].Requests += u.Requests
				out[i].Backoffs += u.Backoffs
				merged = true
				break
			}
		}
		if !merged {
			u.Caller = ""
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
			if u.Provider == d.Provider && u.Fingerprint == d.Fingerprint &&
				u.PeriodStart.Equal(d.PeriodStart) && u.Caller == d.Caller {
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
func quotaRegistry(t *testing.T, quota int, now func() time.Time, opts ...Option) *Registry {
	t.Helper()
	setBuiltinLimit(t, "test", Limit{RPS: 1000, Burst: 100, Quota: quota, Period: QuotaMonth})
	opts = append([]Option{WithClock(now)}, opts...)
	return NewRegistry(opts...)
}

// TestQuotaClassReserve: background work stops at the reserve so an interactive
// request still has allowance left. This is the difference between a sweep
// eating the month and a person being told "no" on the 20th.
func TestQuotaClassReserve(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(t, 10, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key"}
	b := reg.bucket(cred)

	// backgroundReserve is 0.8, so background gets 8 of 10.
	for i := range 8 {
		require.NoError(t, b.reserve(spend.ClassBackground, spend.Unlabelled, now), "background request %d", i)
	}
	require.ErrorIs(t, b.reserve(spend.ClassBackground, spend.Unlabelled, now), ErrQuotaExhausted)
	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now), "the reserve is for interactive work")
	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	require.ErrorIs(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now), ErrQuotaExhausted, "hard ceiling for everyone")
}

// TestQuotaPeriodRollover: the allowance resets on the provider's calendar
// boundary, not on a rolling window.
func TestQuotaPeriodRollover(t *testing.T) {
	now := time.Date(2026, 7, 31, 23, 59, 0, 0, time.UTC)
	reg := quotaRegistry(t, 2, func() time.Time { return now })
	b := reg.bucket(Credential{Provider: "test", APIKey: "key"})

	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	require.ErrorIs(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now), ErrQuotaExhausted)

	next := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, next), "a new month is a new allowance")
}

// TestRemainingSizesTheSweep: the portion a sweep may spend comes from what is
// left of the plan, so changing the plan changes the budget with it.
func TestRemainingSizesTheSweep(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(t, 1000, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key"}

	left, end, ok := reg.Remaining(cred)
	require.True(t, ok)
	assert.Equal(t, 800, left, "background may spend the reserve share")
	assert.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), end)

	require.NoError(t, reg.bucket(cred).reserve(spend.ClassBackground, spend.Unlabelled, now))
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

	reg := quotaRegistry(t, 10, clock, WithUsageStore(store))
	require.NoError(t, reg.Start(ctx))
	b := reg.bucket(cred)
	for range 6 {
		require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	}
	require.NoError(t, reg.Stop(ctx))

	restarted := quotaRegistry(t, 10, clock, WithUsageStore(store))
	require.NoError(t, restarted.Start(ctx))
	got := restarted.bucket(cred).snapshot()
	assert.EqualValues(t, 6, got.Requests, "spend must survive the restart")
	assert.Equal(t, fingerprint("key"), got.Fingerprint, "the key itself is never stored")
	assert.NotContains(t, got.Fingerprint, "key")

	// Restored spend counts against the background reserve (8 of 10): two more
	// background requests fit, the third does not.
	require.NoError(t, restarted.bucket(cred).reserve(spend.ClassBackground, spend.Unlabelled, now))
	require.NoError(t, restarted.bucket(cred).reserve(spend.ClassBackground, spend.Unlabelled, now))
	require.ErrorIs(t, restarted.bucket(cred).reserve(spend.ClassBackground, spend.Unlabelled, now), ErrQuotaExhausted)
}

// TestFlushSendsOnlyDeltas: counters are added to, not set, so two backend
// instances sum the way the provider sums them.
func TestFlushSendsOnlyDeltas(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{}
	reg := quotaRegistry(t, 100, func() time.Time { return now }, WithUsageStore(store))
	b := reg.bucket(Credential{Provider: "test", APIKey: "key"})

	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	require.NoError(t, reg.Flush(ctx))
	require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	require.NoError(t, reg.Flush(ctx))
	require.NoError(t, reg.Flush(ctx), "nothing pending is not a write")

	require.Len(t, store.added, 2)
	assert.EqualValues(t, 1, store.added[0][0].Requests)
	assert.EqualValues(t, 1, store.added[1][0].Requests)
	assert.EqualValues(t, 2, store.rows[0].Requests)
}

// TestSpendIsSplitByCaller: the counter says how much of a plan went, the
// caller label says what spent it. Two callers on one key are flushed apart and
// still summed for the quota gate, which meters the key, not the caller.
func TestSpendIsSplitByCaller(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := &fakeUsageStore{}
	reg := quotaRegistry(t, 5, func() time.Time { return now }, WithUsageStore(store))
	client := &http.Client{Transport: reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil)}

	get := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	sweep := spend.WithCaller(spend.WithClass(ctx, spend.ClassBackground), "job:price_sweep")
	guard := spend.WithCaller(sweep, "contract_guard")
	require.NoError(t, get(sweep))
	require.NoError(t, get(guard))
	require.NoError(t, get(guard))
	require.NoError(t, get(ctx))

	// 4 of the 5-request background ceiling (80%) are spent across three callers.
	require.ErrorIs(t, get(sweep), ErrQuotaExhausted, "the gate reads the key's total")

	require.NoError(t, reg.Flush(ctx))
	require.Len(t, store.added, 1)
	byCaller := map[string]int64{}
	for _, d := range store.added[0] {
		byCaller[d.Caller] += d.Requests
	}
	assert.Equal(t, map[string]int64{
		"job:price_sweep":                1,
		"job:price_sweep/contract_guard": 2,
		spend.Unlabelled:                 1,
	}, byCaller)
}

// TestTierSelectsLimit: the plan named on the account picks the limit, and an
// unnamed one is inferred from whether a key is present at all.
func TestTierSelectsLimit(t *testing.T) {
	reg := NewRegistry()

	keyless := reg.limitFor(Credential{Provider: "coingecko"})
	demo := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key"})
	pro := reg.limitFor(Credential{Provider: "coingecko", APIKey: "key", Tier: "pro"})

	assert.Zero(t, keyless.Quota, "the keyless tier meters rate, not volume")
	assert.Equal(t, 10000, demo.Quota, "free keyed plan is the demo allowance")
	assert.Equal(t, QuotaMonth, demo.Period)
	assert.Greater(t, pro.Quota, demo.Quota)

	assert.Same(t,
		reg.bucket(Credential{Provider: "coingecko", APIKey: "key"}),
		reg.bucket(Credential{Provider: "coingecko", APIKey: "key", Tier: "pro"}),
		"a tier change is a new ceiling on the same key's spend")
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
	reg := quotaRegistry(t, 1, func() time.Time { return now })
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

// TestRepeatedBackoffEscalates: a provider that keeps refusing buys a longer
// pause each time, so an instance that has run into a spent plan stops paying
// for the same answer. Dev spent four and a half days re-asking CoinGecko for a
// 429 it had already been given seventy times.
func TestRepeatedBackoffEscalates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	reg := newTestRegistry(t, 1000)
	tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil).(*limitedTransport)
	require.True(t, ok)

	now := time.Now()
	tr.now = func() time.Time { return now }
	client := &http.Client{Transport: tr}

	// Each call is made from a moment past the previous freeze, so the pause
	// being measured is the new one rather than what was left of the old.
	for _, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		resp, err := client.Get(srv.URL) //nolint:noctx // fixed URL
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, want, tr.bucket.frozenFor(now), "streak of refusals lengthens the pause")
		now = now.Add(want)
	}
}

// TestSuccessResetsEscalation: the escalation is a response to being refused,
// not a memory of having been refused. One answer that is not a refusal puts
// the next pause back where it started.
func TestSuccessResetsEscalation(t *testing.T) {
	var refuse atomic.Bool
	refuse.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if refuse.Load() {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := newTestRegistry(t, 1000)
	tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil).(*limitedTransport)
	require.True(t, ok)

	now := time.Now()
	tr.now = func() time.Time { return now }
	client := &http.Client{Transport: tr}

	for range 3 {
		resp, err := client.Get(srv.URL) //nolint:noctx // fixed URL
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		now = now.Add(tr.bucket.frozenFor(now))
	}
	require.EqualValues(t, 3, tr.bucket.streak)

	refuse.Store(false)
	resp, err := client.Get(srv.URL) //nolint:noctx // fixed URL
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.EqualValues(t, 0, tr.bucket.streak, "a good answer ends the run")

	refuse.Store(true)
	resp, err = client.Get(srv.URL) //nolint:noctx // fixed URL
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, time.Minute, tr.bucket.frozenFor(now),
		"the next refusal starts from the provider's own pause again")
}

// TestEscalationIsBounded: however long a provider stays unavailable, the pause
// stops growing. Believing a provider will never come back is a claim, and this
// package does not make claims — it only stops paying for a refused answer.
func TestEscalationIsBounded(t *testing.T) {
	assert.Equal(t, maxEscalatedBackoff, escalate(time.Minute, defaultBackoff, 1000))
	assert.Equal(t, time.Minute, escalate(time.Minute, defaultBackoff, 1), "one refusal is the provider's own pause")
	assert.Equal(t, defaultBackoff, escalate(0, defaultBackoff, 1), "a provider that names no pause gets the default")
}

// TestEscalationFromZeroTakesTheFallback: Retry-After: 0 or a past date starts
// from the plan's own pause, not the default minute, and doubles from there.
func TestEscalationFromZeroTakesTheFallback(t *testing.T) {
	assert.Equal(t, time.Second, escalate(0, time.Second, 1))
	assert.Equal(t, 4*time.Second, escalate(0, time.Second, 3))
}

// TestUnusableReportsWhyAndWhen: the sweep asks this before it selects targets,
// so the answer has to distinguish the two reasons a credential can be useless.
// "Come back next month" and "come back after lunch" are different instructions.
func TestUnusableReportsWhyAndWhen(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(t, 10, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key"}

	_, unusable := reg.Unusable(cred)
	assert.False(t, unusable, "a credential that has spent nothing is usable")

	// Background may spend 8 of 10; spending them all leaves the sweep nothing.
	b := reg.bucket(cred)
	for range 8 {
		require.NoError(t, b.reserve(spend.ClassBackground, spend.Unlabelled, now))
	}
	reason, unusable := reg.Unusable(cred)
	require.True(t, unusable)
	assert.Contains(t, reason, "share of the plan is spent")
	assert.Contains(t, reason, "2026-09-01", "a spent share ends on the period boundary")

	_, unusable = reg.Unusable(Credential{Provider: "test", APIKey: "another"})
	assert.False(t, unusable,
		"a plan is metered per credential: one being spent says nothing about the next")
}

// TestUnusableReportsAPause: a run of refusals is the other reason, and it ends
// at a moment rather than on a calendar boundary.
func TestUnusableReportsAPause(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	setBuiltinLimit(t, "test", Limit{RPS: 1000, Burst: 10})
	reg := NewRegistry(WithClock(func() time.Time { return now }))
	cred := Credential{Provider: "test", APIKey: "key"}

	reg.bucket(cred).freezeUntil(now.Add(30 * time.Minute))
	reason, unusable := reg.Unusable(cred)
	require.True(t, unusable)
	assert.Contains(t, reason, "paused after repeated refusals")
	assert.Contains(t, reason, "12:30")
}

// TestBudgetHandleReportsUnusable: an adapter holds a Budget, not the registry,
// so that is where the question has to be answerable.
func TestBudgetHandleReportsUnusable(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(t, 10, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key"}
	budget := reg.Budget(cred)

	_, unusable := budget.Unusable()
	assert.False(t, unusable)

	b := reg.bucket(cred)
	for range 8 {
		require.NoError(t, b.reserve(spend.ClassBackground, spend.Unlabelled, now))
	}
	_, unusable = budget.Unusable()
	assert.True(t, unusable)

	var nilBudget *Budget
	_, unusable = nilBudget.Unusable()
	assert.False(t, unusable, "no budget wired means nothing to report")
}

// TestRejectedKeyIsUnusableUntilItIsAnswered: a provider refusing the key
// itself is a third reason a credential cannot carry work, and the one its
// owner has to repair; before it was recorded, a revoked key read as healthy
// for as long as it stayed revoked. One answer that is not a refusal clears it.
func TestRejectedKeyIsUnusableUntilItIsAnswered(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusUnauthorized)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()

	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	setBuiltinLimit(t, "test", Limit{RPS: 1000, Burst: 10})
	reg := NewRegistry(WithClock(func() time.Time { return now }))
	cred := Credential{Provider: "test", APIKey: "key"}
	client := &http.Client{Transport: reg.Transport(cred, nil)}
	get := func() {
		t.Helper()
		resp, err := client.Get(srv.URL) //nolint:noctx // fixed URL
		require.NoError(t, err, "the refusal reaches the adapter as a response")
		require.NoError(t, resp.Body.Close())
	}

	get()
	reason, until, unusable := reg.Budget(cred).UnusableUntil()
	require.True(t, unusable)
	assert.Contains(t, reason, "rejected this credential")
	assert.Contains(t, reason, "401")
	assert.Equal(t, now.Add(rejectedRetry), until)

	status.Store(http.StatusOK)
	get()
	_, unusable = reg.Budget(cred).Unusable()
	assert.False(t, unusable, "an accepted request clears the rejection")

	// A 403 is a host or firewall block, which every key meets alike.
	status.Store(http.StatusForbidden)
	get()
	_, unusable = reg.Budget(cred).Unusable()
	assert.False(t, unusable, "a block is not a dead key")

	// No key, no credential to reject.
	status.Store(http.StatusUnauthorized)
	keyless := Credential{Provider: "test"}
	client = &http.Client{Transport: reg.Transport(keyless, nil)}
	get()
	_, unusable = reg.Budget(keyless).Unusable()
	assert.False(t, unusable)
}

// TestCostDrawsItsUnitsFromTheBucket: a request that costs n units takes n
// tokens, so a provider metering compute units is paced in them.
func TestCostDrawsItsUnitsFromTheBucket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()

	reg := newTestRegistry(t, 1000)
	tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key", Limit: Limit{RPS: 1, Burst: 100}}, nil).(*limitedTransport)
	require.True(t, ok)

	send := func(cost int) {
		req, err := http.NewRequestWithContext(WithCost(t.Context(), cost), http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := (&http.Client{Transport: tr}).Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	send(60)
	assert.InDelta(t, 40, tr.bucket.limiter.Tokens(), 1, "60 of 100 taken")

	// Past the burst the cost is taken a full bucket at a time, so it is
	// charged in full: 250 units at 1000 a second, burst 100, refill 0.15 s.
	reg = newTestRegistry(t, 1000)
	tr, ok = reg.Transport(Credential{Provider: "fast", APIKey: "key", Limit: Limit{RPS: 1000, Burst: 100}}, nil).(*limitedTransport)
	require.True(t, ok)
	start := time.Now()
	send(250) // send reads tr when called: a cost past the burst waits, it is not refused
	assert.GreaterOrEqual(t, time.Since(start), 140*time.Millisecond,
		"150 units past the burst take their refill; a cost clamped to the burst would not wait")
}

// TestRequestWithoutCostCostsOne: providers that meter requests are untouched.
func TestRequestWithoutCostCostsOne(t *testing.T) {
	assert.Equal(t, 1, costOf(t.Context()))
	assert.Equal(t, 1, costOf(WithCost(t.Context(), 0)))
	assert.Equal(t, 120, costOf(WithCost(t.Context(), 120)))
}

// TestBackoffFollowsTheProvider: a refusal without Retry-After, or with
// Retry-After: 0, pauses for the provider's own Backoff, a second; providers
// without one keep the minute.
func TestBackoffFollowsTheProvider(t *testing.T) {
	for _, tc := range []struct {
		limit      Limit
		retryAfter string
		want       time.Duration
	}{
		{Limit{RPS: 1000, Burst: 10, Backoff: time.Second}, "", time.Second},
		{Limit{RPS: 1000, Burst: 10, Backoff: time.Second}, "0", time.Second},
		{Limit{RPS: 1000, Burst: 10}, "", defaultBackoff},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		reg := newTestRegistry(t, 1000)
		tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key", Limit: tc.limit}, nil).(*limitedTransport)
		require.True(t, ok)
		now := time.Now()
		tr.now = func() time.Time { return now }
		resp, err := (&http.Client{Transport: tr}).Get(srv.URL) //nolint:noctx // fixed URL
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		srv.Close()
		assert.Equal(t, tc.want, tr.bucket.frozenFor(now), "Retry-After %q", tc.retryAfter)
	}
}

// TestAlchemyIsPacedInComputeUnits: the free tier allows 300 CU/s for the
// whole account; this deployment keeps under half of it, and a refusal pauses
// seconds, not a minute.
func TestAlchemyIsPacedInComputeUnits(t *testing.T) {
	l := defaultLimits["alchemy"]
	assert.LessOrEqual(t, l.RPS, 150.0, "under half of 300 CU/s")
	assert.GreaterOrEqual(t, l.Burst, 400, "a 20-block batch (400 CU) fits one bucket")
	assert.LessOrEqual(t, float64(l.Burst), 10*l.RPS, "no more than the 10 s window holds")
	assert.Equal(t, time.Second, l.Backoff)
}

// TestFreezePastTheDeadlineFailsAtOnce: a request whose deadline falls inside
// the freeze is refused at once, not after sleeping to its timeout, and still
// reads as a deadline; one whose deadline outlasts the freeze waits it out.
func TestFreezePastTheDeadlineFailsAtOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	reg := newTestRegistry(t, 1000)
	tr, ok := reg.Transport(Credential{Provider: "test", APIKey: "key"}, nil).(*limitedTransport)
	require.True(t, ok)

	tr.bucket.freezeUntil(time.Now().Add(time.Hour))
	start := time.Now()
	_, err := (&http.Client{Transport: tr, Timeout: 30 * time.Second}).Get(srv.URL) //nolint:noctx // the client timeout is the deadline
	require.ErrorIs(t, err, ErrFrozen)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second)

	tr, ok = reg.Transport(Credential{Provider: "test", APIKey: "other"}, nil).(*limitedTransport)
	require.True(t, ok)
	tr.bucket.freezeUntil(time.Now().Add(50 * time.Millisecond))
	resp, err := (&http.Client{Transport: tr, Timeout: 30 * time.Second}).Get(srv.URL) //nolint:noctx // the client timeout is the deadline
	require.NoError(t, err, "a freeze inside the deadline is waited out")
	require.NoError(t, resp.Body.Close())
}
