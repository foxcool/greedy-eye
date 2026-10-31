package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/foxcool/greedy-eye/internal/spend"
)

// countingServer answers 200 and counts what reached it.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// send makes one request through a client built for c, the way the resolver
// builds a fresh client per account per sync.
func send(t *testing.T, reg *Registry, c Credential, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: reg.Transport(c, nil)}).Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// TestQuotaChangeReachesLiveBucket: an account's quota is edited while the
// process runs, and the next client built from it must be held to the new
// share.
func TestQuotaChangeReachesLiveBucket(t *testing.T) {
	srv, hits := countingServer(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(1000, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key", Account: "acc-1", Limit: Limit{Quota: 10, Period: QuotaMonth}}

	for range 6 {
		require.NoError(t, send(t, reg, cred, srv.URL))
	}

	lowered := cred
	lowered.Limit.Quota = 5
	require.ErrorIs(t, send(t, reg, lowered, srv.URL), ErrQuotaExhausted, "6 spent is over a share of 5")
	assert.EqualValues(t, 6, hits.Load())

	left, _, ok := reg.Remaining(lowered)
	require.True(t, ok)
	assert.Zero(t, left)

	raised := cred
	raised.Limit.Quota = 20
	require.NoError(t, send(t, reg, raised, srv.URL), "the account's own raise applies too")
}

// TestTierChangeKeepsSpend: the provider meters the key, and so does
// provider_usage — the tier is not part of either. Moving a key to another plan
// mid-month must not forget what it spent since the process started.
func TestTierChangeKeepsSpend(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{rows: []Usage{{
		Provider: "coingecko", Fingerprint: fingerprint("key"),
		PeriodStart: periodStart(now, QuotaMonth), Requests: 100,
	}}}
	reg := NewRegistry(nil, WithClock(func() time.Time { return now }), WithUsageStore(store))
	require.NoError(t, reg.Start(ctx))
	t.Cleanup(func() { _ = reg.Stop(ctx) })

	demo := Credential{Provider: "coingecko", APIKey: "key", Account: "acc-1"}
	b := reg.bucket(demo)
	for range 50 {
		require.NoError(t, b.reserve(spend.ClassInteractive, spend.Unlabelled, now))
	}

	pro := Credential{Provider: "coingecko", APIKey: "key", Account: "acc-1", Tier: "pro"}
	left, _, ok := reg.Remaining(pro)
	require.True(t, ok)
	proCeiling := int(float64(defaultLimits["coingecko:pro"].Quota) * backgroundReserve)
	assert.Equal(t, proCeiling-150, left, "restored 100 plus 50 spent since start")
	assert.Equal(t, rate.Limit(defaultLimits["coingecko:pro"].RPS), reg.bucket(pro).limiter.Limit(),
		"the new plan's rate applies too")
}

// TestKeylessLimitIgnoresAccount: every key-less credential of a provider draws
// on one per-IP bucket shared by the whole deployment. A user account without
// a key — here a cbr account carrying its own numbers — must not be able to
// loosen or tighten it for everyone else, whichever is built first.
func TestKeylessLimitIgnoresAccount(t *testing.T) {
	operator := Limit{RPS: 0.5, Burst: 1}
	system := Credential{Provider: "cbr"}
	user := Credential{
		Provider: "cbr", Account: "user-acc", Tier: "pro",
		Limit: Limit{RPS: 100, Burst: 50, Quota: 1, Period: QuotaDay},
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	for name, order := range map[string][]Credential{
		"system first": {system, user},
		"user first":   {user, system},
	} {
		t.Run(name, func(t *testing.T) {
			reg := NewRegistry(map[string]Limit{"cbr": operator}, WithClock(func() time.Time { return now }))
			for _, c := range order {
				reg.Transport(c, nil)
			}
			b := reg.bucket(system)
			assert.Same(t, b, reg.bucket(user), "key-less spend is one per-IP budget")
			assert.Equal(t, rate.Limit(operator.RPS), b.limiter.Limit())
			assert.Equal(t, operator.Burst, b.limiter.Burst())

			_, _, metered := reg.Remaining(user)
			assert.False(t, metered, "a user's quota must not meter the shared bucket")
			for range 3 {
				require.NoError(t, b.reserve(spend.ClassBackground, spend.Unlabelled, now))
			}
		})
	}
}

// TestSmallestShareWins: two accounts on one key with different shares. Both
// are enforced, so neither can spend past the other's share — the rule recorded
// for the owner in the change that introduced it.
func TestSmallestShareWins(t *testing.T) {
	srv, hits := countingServer(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(1000, func() time.Time { return now })
	wide := Credential{Provider: "test", APIKey: "key", Account: "wide", Limit: Limit{RPS: 50, Quota: 100, Period: QuotaMonth}}
	narrow := Credential{Provider: "test", APIKey: "key", Account: "narrow", Limit: Limit{RPS: 5, Quota: 5, Period: QuotaMonth}}

	reg.Transport(narrow, nil)
	for range 10 {
		_ = send(t, reg, wide, srv.URL)
	}
	assert.EqualValues(t, 5, hits.Load(), "the wide account is held to the narrow share")
	assert.Equal(t, rate.Limit(5), reg.bucket(wide).limiter.Limit(), "and to the slower rate")
}

// TestPeriodAlternationCannotBypassQuota: two accounts on one key, one metered
// by day and one by month, presented in turn. Switching between them must not
// hand the key a fresh counter each time.
func TestPeriodAlternationCannotBypassQuota(t *testing.T) {
	srv, hits := countingServer(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(1000, func() time.Time { return now })
	daily := Credential{Provider: "test", APIKey: "key", Account: "daily", Limit: Limit{Quota: 10, Period: QuotaDay}}
	monthly := Credential{Provider: "test", APIKey: "key", Account: "monthly", Limit: Limit{Quota: 10, Period: QuotaMonth}}

	for i := range 20 {
		c := daily
		if i%2 == 1 {
			c = monthly
		}
		_ = send(t, reg, c, srv.URL)
	}
	assert.EqualValues(t, 10, hits.Load(), "a quota of 10 lets 10 through, however it is presented")
}

// TestPeriodSwitchKeepsSpendSinceStart: an account moves from a monthly to a
// daily share and back. What this process spent since Start counts in both
// windows; going back to what Start loaded would forget it.
func TestPeriodSwitchKeepsSpendSinceStart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{}
	reg := quotaRegistry(1000, func() time.Time { return now }, WithUsageStore(store))
	require.NoError(t, reg.Start(ctx))
	t.Cleanup(func() { _ = reg.Stop(ctx) })

	monthly := Credential{Provider: "test", APIKey: "key", Account: "acc-1", Limit: Limit{Quota: 10, Period: QuotaMonth}}
	daily := monthly
	daily.Limit = Limit{Quota: 8, Period: QuotaDay}

	srv, hits := countingServer(t)
	for range 6 {
		require.NoError(t, send(t, reg, monthly, srv.URL))
	}
	require.NoError(t, reg.Flush(ctx), "a flush in between must not change the count either")

	for range 5 {
		_ = send(t, reg, daily, srv.URL)
	}
	assert.EqualValues(t, 8, hits.Load(), "6 of today's 8 were already spent")

	for range 5 {
		_ = send(t, reg, monthly, srv.URL)
	}
	assert.EqualValues(t, 10, hits.Load(), "back on the month, all 8 count against 10")
}

// TestPendingKeepsItsPeriodAcrossRollover: spend reserved before a month
// boundary and flushed after it belongs to the month it was spent in.
// Stamping it with the new month charges the new allowance for the old one's
// requests.
func TestPendingKeepsItsPeriodAcrossRollover(t *testing.T) {
	ctx := context.Background()
	july := time.Date(2026, 7, 31, 23, 59, 30, 0, time.UTC)
	august := time.Date(2026, 8, 1, 0, 0, 10, 0, time.UTC)
	now := july
	store := &fakeUsageStore{}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	cred := Credential{Provider: "test", APIKey: "key"}
	srv, _ := countingServer(t)

	require.NoError(t, send(t, reg, cred, srv.URL))
	now = august
	require.NoError(t, send(t, reg, cred, srv.URL))
	require.NoError(t, reg.Flush(ctx))

	byPeriod := map[time.Time]int64{}
	for _, u := range store.rows {
		byPeriod[u.PeriodStart] += u.Requests
	}
	assert.Equal(t, map[time.Time]int64{
		periodStart(july, QuotaMonth):   1,
		periodStart(august, QuotaMonth): 1,
	}, byPeriod)
}

// TestPendingKeepsItsPeriodAcrossSwitch: the same for an account moving from a
// monthly to a daily share between two flushes.
func TestPendingKeepsItsPeriodAcrossSwitch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{}
	reg := quotaRegistry(100, func() time.Time { return now }, WithUsageStore(store))
	monthly := Credential{Provider: "test", APIKey: "key", Account: "acc-1", Limit: Limit{Quota: 50, Period: QuotaMonth}}
	daily := monthly
	daily.Limit = Limit{Quota: 50, Period: QuotaDay}
	srv, _ := countingServer(t)

	require.NoError(t, send(t, reg, monthly, srv.URL))
	require.NoError(t, send(t, reg, daily, srv.URL))
	require.NoError(t, reg.Flush(ctx))

	byPeriod := map[time.Time]int64{}
	for _, u := range store.rows {
		byPeriod[u.PeriodStart] += u.Requests
	}
	assert.Equal(t, map[time.Time]int64{
		periodStart(now, QuotaMonth): 1,
		periodStart(now, QuotaDay):   1,
	}, byPeriod)
}

// TestRetainedBudgetCannotUndoAnEdit: a client built before an account edit can
// still be finishing a sweep when the edit lands. Asking its Budget must read
// the live bucket, not put the old share back on it.
func TestRetainedBudgetCannotUndoAnEdit(t *testing.T) {
	srv, hits := countingServer(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	reg := quotaRegistry(1000, func() time.Time { return now })
	cred := Credential{Provider: "test", APIKey: "key", Account: "acc-1", Limit: Limit{Quota: 10, Period: QuotaMonth}}
	old := reg.Budget(cred)
	for range 6 {
		require.NoError(t, send(t, reg, cred, srv.URL))
	}

	lowered := cred
	lowered.Limit.Quota = 5
	client := &http.Client{Transport: reg.Transport(lowered, nil)}

	left, _, ok := old.Remaining()
	require.True(t, ok)
	assert.Zero(t, left, "6 spent is over the lowered share")
	_, unusable := old.Unusable()
	assert.True(t, unusable)
	_, _, unusable = old.UnusableUntil()
	assert.True(t, unusable)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.ErrorIs(t, err, ErrQuotaExhausted, "the edit still holds after the old handle was asked")
	assert.EqualValues(t, 6, hits.Load())
}

// TestRemainingAnswersWithTheTightestPace: a sweep divides what is left by the
// time to the period's end, so the entry that affords the least per hour must
// answer, not the one with the smallest raw balance. Background work gets
// backgroundReserve (0.8) of a quota, so on July 15 at noon a daily 100 leaves
// 80 for today, about 6.7 an hour, and a monthly 1000 leaves 800, about 2.
func TestRemainingAnswersWithTheTightestPace(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	monthEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	t.Run("smaller balance, faster pace", func(t *testing.T) {
		reg := quotaRegistry(1000, func() time.Time { return now })
		daily := Credential{Provider: "test", APIKey: "key", Account: "daily", Limit: Limit{Quota: 100, Period: QuotaDay}}
		monthly := Credential{Provider: "test", APIKey: "key", Account: "monthly", Limit: Limit{Quota: 1000, Period: QuotaMonth}}
		reg.Transport(daily, nil)
		reg.Transport(monthly, nil)

		left, end, ok := reg.Budget(daily).Remaining()
		require.True(t, ok)
		assert.Equal(t, 800, left)
		assert.Equal(t, monthEnd, end)
	})

	t.Run("equal balances", func(t *testing.T) {
		reg := quotaRegistry(1000, func() time.Time { return now })
		daily := Credential{Provider: "test", APIKey: "key", Account: "daily", Limit: Limit{Quota: 100, Period: QuotaDay}}
		monthly := Credential{Provider: "test", APIKey: "key", Account: "monthly", Limit: Limit{Quota: 100, Period: QuotaMonth}}
		reg.Transport(daily, nil)
		budget := reg.Budget(monthly)

		// Entries live in a map; asking repeatedly is what exposes an answer
		// that depends on iteration order.
		for range 50 {
			_, end, ok := budget.Remaining()
			require.True(t, ok)
			require.Equal(t, monthEnd, end, "80 over the month is the slower pace")
		}
	})

	t.Run("equal pace", func(t *testing.T) {
		reg := quotaRegistry(1000, func() time.Time { return now })
		// 0.8 of 15 is 12 over the 12 hours of today, 0.8 of 495 is 396 over
		// the 396 hours of the month: one request an hour either way.
		daily := Credential{Provider: "test", APIKey: "key", Account: "daily", Limit: Limit{Quota: 15, Period: QuotaDay}}
		monthly := Credential{Provider: "test", APIKey: "key", Account: "monthly", Limit: Limit{Quota: 495, Period: QuotaMonth}}
		reg.Transport(daily, nil)
		budget := reg.Budget(monthly)

		for range 50 {
			left, end, ok := budget.Remaining()
			require.True(t, ok)
			require.Equal(t, 12, left, "the earlier end answers")
			require.Equal(t, time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), end)
		}
	})
}
