//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProviderUsageAccumulates: counters are added to, never set, so two
// backend instances (or two flushes) sum the way the provider sums them, and a
// restart reads back what was already spent.
func TestProviderUsageAccumulates(t *testing.T) {
	pool := getTestPool(t)
	s := NewProviderUsageStore(pool)
	ctx := context.Background()

	period := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	other := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	delta := func(fp string, start time.Time, requests, backoffs int64) ratelimit.Usage {
		return ratelimit.Usage{
			Provider:    "coingecko",
			Fingerprint: fp,
			PeriodStart: start,
			Requests:    requests,
			Backoffs:    backoffs,
		}
	}

	require.NoError(t, s.AddUsage(ctx, []ratelimit.Usage{
		delta("abc123", period, 40, 1),
		delta("def456", period, 7, 0),
		delta("abc123", other, 5, 0),
	}))
	require.NoError(t, s.AddUsage(ctx, []ratelimit.Usage{delta("abc123", period, 2, 1)}))

	got, err := s.LoadUsage(ctx, period)
	require.NoError(t, err)
	require.Len(t, got, 2, "the other period must not leak in")

	byFingerprint := map[string]ratelimit.Usage{}
	for _, u := range got {
		byFingerprint[u.Fingerprint] = u
	}
	assert.EqualValues(t, 42, byFingerprint["abc123"].Requests, "deltas add up")
	assert.EqualValues(t, 2, byFingerprint["abc123"].Backoffs)
	assert.EqualValues(t, 7, byFingerprint["def456"].Requests)
	assert.Equal(t, period.UTC(), byFingerprint["abc123"].PeriodStart.UTC())

	t.Run("empty batch is a no-op", func(t *testing.T) {
		require.NoError(t, s.AddUsage(ctx, nil))
	})

	t.Run("unknown period reads empty", func(t *testing.T) {
		rows, err := s.LoadUsage(ctx, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})
}
