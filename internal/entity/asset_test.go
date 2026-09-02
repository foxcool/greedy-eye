package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsListedVenue draws the line the scam filter trusts: a market that names
// an exchange publishing its own instrument catalogue, against everything a
// token's issuer can mint for itself.
func TestIsListedVenue(t *testing.T) {
	listed := []string{"moex", "spbex", "nasdaq", "MOEX", "  moex  "}
	for _, m := range listed {
		assert.True(t, IsListedVenue(m), "market %q", m)
	}

	notListed := []string{
		"",
		MarketCrypto,
		MarketForex,
		ContractMarket("eth", "0xdac17f958d2ee523a2206206994597c13d831ec7"),
		"onchain:ton/0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe",
	}
	for _, m := range notListed {
		assert.False(t, IsListedVenue(m), "market %q", m)
	}
}
