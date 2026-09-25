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

// TestNormalizeName pins what Postgres text cannot hold: a NUL byte and invalid
// UTF-8 both become U+FFFD instead of failing the write or disappearing, so the
// position lands and the marker is still there for the scam filter to judge.
func TestNormalizeName(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain name unchanged":      {"Tether USD", "Tether USD"},
		"surrounding space trimmed": {"  GTPS ", "GTPS"},
		"NUL byte marked":           {"GTPS\x00", "GTPS�"},
		"invalid UTF-8 marked":      {"BUY\xffSAFU", "BUY�SAFU"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, NormalizeName(c.in))
		})
	}
	assert.Equal(t, "GT�PS", NormalizeSymbol(" gt\x00ps"), "symbols get the same treatment before uppercasing")
}
