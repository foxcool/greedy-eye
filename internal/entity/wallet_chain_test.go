package entity

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFailedChains_ReadsEveryBranchOfAJoin: a syncer joins one error per
// chain, and the chains that failed whole are what the account's watchers
// count. An item-level error on a chain that answered is not among them.
func TestFailedChains_ReadsEveryBranchOfAJoin(t *testing.T) {
	err := errors.Join(
		&ChainError{Chain: "hydration", Err: errors.New("subscan API status 404")},
		fmt.Errorf("address 0xabc: %w", &ChainError{Chain: "astar", Err: errors.New("timeout")}),
		errors.New("chain eth, token USDT: no decimals reported"),
		&ChainError{Chain: "hydration", Err: errors.New("second complaint")},
	)

	assert.Equal(t, map[string]string{
		"hydration": "subscan API status 404",
		"astar":     "timeout",
	}, FailedChains(err))
	assert.Nil(t, FailedChains(errors.New("chain eth, token X: bad")))
	assert.Nil(t, FailedChains(nil))
	assert.Equal(t, "hydration: boom", (&ChainError{Chain: "hydration", Err: errors.New("boom")}).Error())
}
