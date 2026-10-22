package entity

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScopeFailures sorts a wallet read's error by what it left unread: a whole
// chain, one position, or nothing it can name.
func TestScopeFailures(t *testing.T) {
	err := errors.Join(
		&ChainError{Chain: "hydration", Err: errors.New("404")},
		fmt.Errorf("address 2: %w", &ChainError{Chain: "polkadot", Err: errors.New("429")}),
		&EntryError{Chain: "arbitrum", Ref: "0xftw", Err: errors.New("no decimals reported")},
	)
	got := ScopeFailures(err)
	assert.Equal(t, map[string]string{"hydration": "404", "polkadot": "429"}, got.Chains)
	require.Len(t, got.Entries, 1)
	assert.Equal(t, "0xftw", got.Entries[0].Ref)
	assert.False(t, got.Unscoped)

	assert.True(t, ScopeFailures(errors.Join(err, errors.New("esplora status 503"))).Unscoped,
		"an error naming neither chain nor position says nothing about what it left unread")
	assert.True(t, ScopeFailures(&EntryError{Chain: "eth", Err: errors.New("x")}).Unscoped,
		"a refusal without a contract cannot be placed")
	assert.Equal(t, SyncFailures{}, ScopeFailures(nil))
}

type hollow struct{}

func (hollow) Error() string { return "wrapped nothing" }
func (hollow) Unwrap() error { return nil }

// TestScopeFailures_UnplaceableChains: a chain failure with no chain name, or
// an error that classifies as nothing, cannot be scoped to any row.
func TestScopeFailures_UnplaceableChains(t *testing.T) {
	assert.True(t, ScopeFailures(&ChainError{Err: errors.New("rate limit exceeded")}).Unscoped,
		"a batch-level provider error names no chain")
	assert.True(t, ScopeFailures(hollow{}).Unscoped, "a non-nil error left something unread")
}
