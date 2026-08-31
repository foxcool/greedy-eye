package tinvest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures in testdata/ are a sanitised capture of the live broker taken on
// 2026-08-31: account ids are pseudonyms, money is rounded, names are blanked.
// Structure and field names are untouched, because the shape is the whole point.
//
// These tests assert what the WIRE looks like. The mapping rules are exercised
// against a synthetic universe in broker_syncer_test.go — a real capture cannot
// carry the instrument catalogue, which runs to thousands of rows.

func loadCapture(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, into))
}

func capturedPortfolios(t *testing.T) []Portfolio {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "portfolio_*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "the capture is what makes these tests worth running")

	out := make([]Portfolio, 0, len(files))
	for _, f := range files {
		var p Portfolio
		loadCapture(t, filepath.Base(f), &p)
		out = append(out, p)
	}
	return out
}

// TestCaptureParsesIntoTheWireTypes is the claim the fixtures exist to support:
// the types this adapter declares actually match what the broker sends.
func TestCaptureParsesIntoTheWireTypes(t *testing.T) {
	kinds := map[string]int{}
	for _, p := range capturedPortfolios(t) {
		assert.NotEmpty(t, p.AccountID)
		for _, pos := range p.Positions {
			require.NotEmpty(t, pos.FIGI, "every position is identified")
			require.NotEmpty(t, pos.InstrumentType)
			require.NotEmpty(t, pos.ClassCode, "the board is present on every line")
			_, ok := pos.Quantity.Decimal()
			assert.True(t, ok, "quantity of %s is readable", pos.FIGI)
			kinds[pos.InstrumentType]++
		}
	}

	// The same composition the 2026-08-27 measurement recorded, which is how we
	// know this is the same portfolio and not a different slice of it.
	assert.Equal(t, map[string]int{"share": 34, "currency": 7, "etf": 4, "bond": 2}, kinds)
}

// TestCapturedBlockingIsWholeLine records what the API actually does, so nobody
// later reads the two-row split as observed behaviour.
//
// Every blocked position in the capture carries blockedLots = 0: the broker
// says THAT a line is restricted, never how much of it. splitByLiquidity still
// handles a partial quantity because the field exists and would otherwise be
// silently ignored — but that branch is unexercised by this broker today.
func TestCapturedBlockingIsWholeLine(t *testing.T) {
	blocked := 0
	for _, p := range capturedPortfolios(t) {
		for _, pos := range p.Positions {
			lots, ok := pos.BlockedLots.Decimal()
			require.True(t, ok)
			assert.True(t, lots.IsZero(),
				"%s reports a partial block — the split is no longer hypothetical, revisit the test above", pos.FIGI)
			if pos.Blocked {
				blocked++
			}
		}
	}
	assert.Equal(t, 7, blocked, "the frozen foreign paper, seen from the broker's side")
}

// TestCapturedTickersAreOftenISINs is why identity is the instrument id and
// never the ticker: matching on that string would attach a position to whatever
// asset happens to share it.
//
// The capture also shows what the id field really holds, which is NOT always a
// FIGI. Nine positions carry an ISIN in the ticker, and they split two ways:
//
//   - five have a synthetic broker id beside it (TCS00A1055Y4, TCSB43821012) —
//     T-Invest's own namespace, not Bloomberg's
//   - four have the ISIN in BOTH fields: the blocked foreign paper on the OTC
//     boards, for which no FIGI exists at all
//
// Both are stable identifiers within this provider, which is all the ref needs
// to be — asset_external_refs namespaces them under source=tinvest. What would
// break is reading the ticker as identity.
func TestCapturedTickersAreOftenISINs(t *testing.T) {
	isin := regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
	var withOwnID, idIsTheISIN int
	for _, p := range capturedPortfolios(t) {
		for _, pos := range p.Positions {
			if !isin.MatchString(pos.Ticker) {
				continue
			}
			require.NotEmpty(t, pos.FIGI, "a position without an id cannot be bound at all")
			if pos.FIGI == pos.Ticker {
				idIsTheISIN++
				assert.True(t, pos.Blocked,
					"%s has no id of its own; so far that is only true of blocked paper", pos.Ticker)
			} else {
				withOwnID++
			}
		}
	}
	assert.Equal(t, 5, withOwnID)
	assert.Equal(t, 4, idIsTheISIN)
}

// TestCapturedCashNeverStatesItsOwnCurrency pins the trap that a rouble-only
// test cannot see: on a cash line currentPrice is what the position is worth
// in, and for this broker that is always roubles — including for dollars.
func TestCapturedCashNeverStatesItsOwnCurrency(t *testing.T) {
	seen := map[string]string{}
	for _, p := range capturedPortfolios(t) {
		for _, pos := range p.Positions {
			if pos.InstrumentType != InstrumentTypeCurrency {
				continue
			}
			assert.Equal(t, "rub", pos.CurrentPrice.Currency,
				"%s is priced in roubles whatever it holds", pos.Ticker)
			code := currencyCodeOf(pos.Ticker)
			require.NotEmpty(t, code, "ticker %q must yield a currency", pos.Ticker)
			seen[code] = pos.Ticker
		}
	}
	assert.Equal(t, map[string]string{
		"USD": "USD000UTSTOM",
		"RUB": "RUB000UTSTOM",
		"EUR": "EUR_RUB__TOM_CETS",
	}, seen, "three currencies, none of which currentPrice would have named")
}

// TestCapturedBoardsResolveToAMarket: the fallback is only reached for an
// instrument the catalogue does not carry, but when it is reached, every board
// this broker uses for securities has to yield a venue.
func TestCapturedBoardsResolveToAMarket(t *testing.T) {
	for _, p := range capturedPortfolios(t) {
		for _, pos := range p.Positions {
			if pos.InstrumentType == InstrumentTypeCurrency {
				continue // cash never takes this path
			}
			assert.NotEmpty(t, marketOfClassCode(pos.ClassCode),
				"board %q (%s) resolves to no market", pos.ClassCode, pos.Ticker)
		}
	}
}

// TestCapturedAccountsAreReadOnly records the safety the design leaned on: dev
// holds the same token as prod, and it cannot trade.
func TestCapturedAccountsAreReadOnly(t *testing.T) {
	var accounts struct {
		Accounts []BrokerAccount `json:"accounts"`
	}
	loadCapture(t, "accounts.json", &accounts)
	require.Len(t, accounts.Accounts, 3)

	boxes := 0
	for _, a := range accounts.Accounts {
		assert.Equal(t, AccountStatusOpen, a.Status)
		assert.Equal(t, "ACCOUNT_ACCESS_LEVEL_READ_ONLY", a.AccessLevel,
			"reading a real portfolio from dev is only safe while this holds")
		if a.Type == AccountTypeInvestBox {
			boxes++
		}
	}
	assert.Equal(t, 1, boxes, "the round-up pot is recognisable, so it can be left alone")
}

// TestCapturedBondsCarryAVenue: bonds joined the catalogue so a bond POSITION
// has a market to take. A bond universe that did not name a venue would make
// that pointless.
func TestCapturedBondsCarryAVenue(t *testing.T) {
	var bonds struct {
		Instruments []Instrument `json:"instruments"`
	}
	loadCapture(t, "bonds_sample.json", &bonds)
	require.NotEmpty(t, bonds.Instruments)
	for _, b := range bonds.Instruments {
		assert.NotEmpty(t, b.FIGI)
		assert.NotEmpty(t, MarketOf(b), "bond %s settles nowhere this system knows", b.Ticker)
	}
}
