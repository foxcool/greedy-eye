package main

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRateLimitOverrides covers the one thing the config section has to get
// right: a provider the operator did not name must keep its built-in limit
// rather than silently becoming a zero-rate bucket, which would stall every
// request against that provider forever.
func TestRateLimitOverrides(t *testing.T) {
	var cfg Config
	assert.Nil(t, rateLimitOverrides(&cfg), "no section means no overrides")

	cfg.RateLimit = map[string]RateLimitConfig{
		"subscan": {RPS: 0.5, Burst: 1},
	}

	out := rateLimitOverrides(&cfg)
	assert.Len(t, out, 1, "only the named provider is overridden")
	assert.InDelta(t, 0.5, out["subscan"].RPS, 1e-9)
	assert.Equal(t, 1, out["subscan"].Burst)
}

// TestRateLimitOverridesCarryVolume: the section has to be able to express a
// share of a plan, not only a speed. Slowing a sweep down does not reduce what
// a month costs, and when two deployments hold one key that difference is the
// whole problem — each sized its sweep as though it owned the entire plan and
// the plan ran out with both still reporting room.
func TestRateLimitOverridesCarryVolume(t *testing.T) {
	cfg := Config{RateLimit: map[string]RateLimitConfig{
		"coingecko": {Quota: 2000, Period: "month"},
	}}

	out := rateLimitOverrides(&cfg)
	assert.Equal(t, 2000, out["coingecko"].Quota)
	assert.Equal(t, ratelimit.QuotaMonth, out["coingecko"].Period)
	assert.Zero(t, out["coingecko"].RPS, "an unwritten field stays unwritten, so the plan's own rate survives")
	assert.Zero(t, out["coingecko"].Burst)
}

// TestRateLimitVolumeNeedsAPeriod: a quota whose period is unset never rolls
// over — the counters reset on a boundary that does not exist — so the
// allowance is spent once and the provider goes quiet for the life of the
// deployment. Refuse it at startup rather than days later as "prices stopped
// updating".
func TestRateLimitVolumeNeedsAPeriod(t *testing.T) {
	err := validateRateLimits(map[string]RateLimitConfig{
		"coingecko": {Quota: 2000},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never resets")

	assert.NoError(t, validateRateLimits(map[string]RateLimitConfig{
		"coingecko": {Quota: 2000, Period: "month"},
	}), "a volume with a window is a budget")

	assert.NoError(t, validateRateLimits(map[string]RateLimitConfig{
		"subscan": {RPS: 0.5},
	}), "no volume, nothing to validate")
}
