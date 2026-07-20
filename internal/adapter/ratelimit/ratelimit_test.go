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
		client := &http.Client{Transport: reg.Transport("test", "key", nil)}
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

	a := reg.bucket("test", "key-a")
	b := reg.bucket("test", "key-b")
	same := reg.bucket("test", "key-a")

	assert.NotSame(t, a, b)
	assert.Same(t, a, same, "same credential must resolve to one bucket")
}

// TestKeylessTierIsSeparate: the keyless quota is per IP, not per key, and is
// usually tighter — it needs its own bucket and its own limit.
func TestKeylessTierIsSeparate(t *testing.T) {
	reg := NewRegistry(nil)

	assert.NotSame(t, reg.bucket("coingecko", ""), reg.bucket("coingecko", "key"))
	assert.Less(t,
		reg.limitFor("coingecko", "").RPS,
		reg.limitFor("coingecko", "key").RPS,
		"keyless must be the slower tier")
}

func TestLimitResolution(t *testing.T) {
	reg := NewRegistry(map[string]Limit{"subscan": {RPS: 0.5, Burst: 1}})

	assert.Equal(t, 0.5, reg.limitFor("subscan", "key").RPS, "override wins")
	assert.Equal(t, 0.5, reg.limitFor("subscan", "").RPS, "override covers both tiers")
	assert.Equal(t, defaultLimits["moralis"].RPS, reg.limitFor("moralis", "key").RPS)
	assert.Equal(t, fallbackLimit.RPS, reg.limitFor("nobody-knows-this-one", "key").RPS)
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
	tr, ok := reg.Transport("test", "key", nil).(*limitedTransport)
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
	tr, ok := reg.Transport("test", "key", nil).(*limitedTransport)
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
	b := newBucket(Limit{RPS: 1, Burst: 1})

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
	assert.Nil(t, reg.Transport("test", "key", nil))
	assert.Equal(t, http.DefaultTransport, reg.Transport("test", "key", http.DefaultTransport))
}
