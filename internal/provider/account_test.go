package provider

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
)

func providerAccount(data map[string]string) *entity.Account {
	return &entity.Account{ID: "acc-1", Data: data}
}

// TestAccountLimitCarriesAShareOfThePlan: the numbers that carve up a plan live
// with the key the plan belongs to. An operator running two deployments off one
// credential gives each its share here — nothing else can, because the two
// instances cannot see each other's spend.
func TestAccountLimitCarriesAShareOfThePlan(t *testing.T) {
	l := accountLimit(providerAccount(map[string]string{
		"api_key": "CG-x", "quota": "2000", "period": "month",
	}))

	assert.Equal(t, 2000, l.Quota)
	assert.Equal(t, ratelimit.QuotaMonth, l.Period)
	assert.Zero(t, l.RPS, "an unwritten field stays unwritten, so the plan's own rate survives")
	assert.Zero(t, l.Burst)
}

// TestAccountLimitEmptyIsSilence: an account that says nothing about limits must
// leave the built-in plan alone, not zero it.
func TestAccountLimitEmptyIsSilence(t *testing.T) {
	assert.Equal(t, ratelimit.Limit{}, accountLimit(providerAccount(map[string]string{"api_key": "k"})))
}

// TestAccountQuotaNeedsAPeriod: a volume with no window never resets — the
// counters roll on a boundary that does not exist — so the allowance would be
// spent once and the provider would go quiet until someone noticed.
//
// Ignored rather than fatal: this arrives from the database while the process
// runs, and an account can be edited at any moment. Refusing to start would mean
// a mistyped form field takes the service down.
func TestAccountQuotaNeedsAPeriod(t *testing.T) {
	l := accountLimit(providerAccount(map[string]string{"quota": "2000"}))
	assert.Zero(t, l.Quota, "a volume with no window is not a budget")

	l = accountLimit(providerAccount(map[string]string{"quota": "2000", "period": "fortnight"}))
	assert.Zero(t, l.Quota, "and neither is one with a window nobody implements")

	l = accountLimit(providerAccount(map[string]string{"quota": "2000", "period": "day"}))
	assert.Equal(t, ratelimit.QuotaDay, l.Period)
}

// TestAccountLimitIgnoresGarbage: one bad value costs its own field, not the
// account and not the boot.
func TestAccountLimitIgnoresGarbage(t *testing.T) {
	l := accountLimit(providerAccount(map[string]string{
		"rps": "fast", "burst": "-3", "quota": "many", "period": "month",
	}))
	assert.Equal(t, ratelimit.Limit{}, l)

	l = accountLimit(providerAccount(map[string]string{"rps": "0.5", "burst": "oops"}))
	assert.InDelta(t, 0.5, l.RPS, 1e-9, "a readable field survives an unreadable neighbour")
	assert.Zero(t, l.Burst)
}

// TestAccountCredCarriesTierAndLimit: the credential is what the limiter keys a
// budget by, so both the plan name and its custom share have to reach it.
func TestAccountCredCarriesTierAndLimit(t *testing.T) {
	c := accountCred("coingecko", providerAccount(map[string]string{
		"api_key": "CG-x", "tier": "pro", "quota": "400000", "period": "month",
	}))

	assert.Equal(t, "coingecko", c.Provider)
	assert.Equal(t, "CG-x", c.APIKey)
	assert.Equal(t, "pro", c.Tier)
	assert.Equal(t, 400000, c.Limit.Quota)

	// The legacy flag named the same thing before tiers existed.
	c = accountCred("coingecko", providerAccount(map[string]string{"api_key": "k", "pro": "true"}))
	assert.Equal(t, "pro", c.Tier)
}
