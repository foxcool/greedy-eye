package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
