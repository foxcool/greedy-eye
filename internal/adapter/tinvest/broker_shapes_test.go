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

// The fixtures in testdata/ are SYNTHETIC. They reproduce the shapes a live
// capture showed on 2026-08-31 — field names, which field carries what, and the
// three ways an instrument names itself — with invented numbers throughout.
//
// The real capture is deliberately not committed. This repository is public,
// and a sanitised portfolio is not an anonymous one: rounding the quantities
// still leaves totalAmountPortfolio in plain text, and the quantities
// themselves reconstruct from expectedYield / (currentPrice -
// averagePositionPrice) to within a couple of percent. A capture's job is to
// reveal the shape once; the shape then lives in the code, and the payload has
// no reason to outlive the reading.

func loadFixture(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, into))
}

func shapes(t *testing.T) Portfolio {
	t.Helper()
	var p Portfolio
	loadFixture(t, "portfolio_shapes.json", &p)
	require.NotEmpty(t, p.Positions)
	return p
}

// TestFixtureParsesIntoTheWireTypes is what the fixture exists for: the types
// this adapter declares match the field names the broker sends.
func TestFixtureParsesIntoTheWireTypes(t *testing.T) {
	kinds := map[string]int{}
	for _, pos := range shapes(t).Positions {
		require.NotEmpty(t, pos.FIGI, "every position is identified")
		require.NotEmpty(t, pos.InstrumentType)
		require.NotEmpty(t, pos.ClassCode, "the board is present on every line")
		_, ok := pos.Quantity.Decimal()
		assert.True(t, ok, "quantity of %s is readable", pos.FIGI)
		kinds[pos.InstrumentType]++
	}
	for _, kind := range []string{InstrumentTypeShare, InstrumentTypeEtf, InstrumentTypeBond, InstrumentTypeCurrency} {
		assert.Positive(t, kinds[kind], "no %s line to exercise", kind)
	}
}

// TestBlockingIsReportedWholeLine records what the live API does: every blocked
// position it returned carried blockedLots = 0. The broker says THAT a line is
// restricted, never how much of it.
//
// splitByLiquidity still converts a populated blockedLots through the lot size,
// because the field exists and ignoring it silently is its own bug — but that
// branch is unexercised by this broker today, and this test says so rather than
// letting a reader take it for observed behaviour.
func TestBlockingIsReportedWholeLine(t *testing.T) {
	blocked := 0
	for _, pos := range shapes(t).Positions {
		lots, ok := pos.BlockedLots.Decimal()
		require.True(t, ok)
		assert.True(t, lots.IsZero(),
			"%s reports a partial block — revisit splitByLiquidity's unexercised branch", pos.FIGI)
		if pos.Blocked {
			blocked++
		}
	}
	assert.Positive(t, blocked, "no blocked line to exercise")
}

// TestLotsAreNotPieces is the evidence behind the conversion in
// splitByLiquidity: quantity counts pieces, quantityLots counts lots, and
// blockedLots is denominated in the latter. Subtracting one from the other
// reported 9999 of a fully blocked 10000-share holding as sellable.
func TestLotsAreNotPieces(t *testing.T) {
	differing := 0
	for _, pos := range shapes(t).Positions {
		pieces, ok := pos.Quantity.Decimal()
		require.True(t, ok)
		lots, ok := pos.QuantityLots.Decimal()
		require.True(t, ok)
		if !pieces.Equal(lots) {
			differing++
			assert.True(t, pieces.GreaterThan(lots), "%s: a lot holds one piece or more", pos.Ticker)
		}
	}
	assert.Positive(t, differing,
		"without a position where the two units differ, the conversion rests on nothing")
}

// TestTickersAreOftenISINs is why identity is the instrument id and never the
// ticker. The id field is not always a FIGI either: it is a real one, a
// synthetic broker id (TCS00A1055Y4, TCSB43821012), or — for paper with no FIGI
// at all — the ISIN repeated in the ticker. All three are stable within this
// provider, which is what the asset_external_refs namespace makes sufficient.
func TestTickersAreOftenISINs(t *testing.T) {
	isin := regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
	var withOwnID, idIsTheISIN int
	for _, pos := range shapes(t).Positions {
		if !isin.MatchString(pos.Ticker) {
			continue
		}
		require.NotEmpty(t, pos.FIGI, "a position without an id cannot be bound at all")
		if pos.FIGI == pos.Ticker {
			idIsTheISIN++
		} else {
			withOwnID++
		}
	}
	assert.Positive(t, withOwnID, "no ISIN-ticker-with-its-own-id line to exercise")
	assert.Positive(t, idIsTheISIN, "no id-is-the-ISIN line to exercise")
}

// TestCashNeverStatesItsOwnCurrency pins the trap a rouble-only fixture cannot
// see: on a cash line currentPrice is what the position is worth in, and for
// this broker that is always roubles — including for dollars.
func TestCashNeverStatesItsOwnCurrency(t *testing.T) {
	seen := map[string]bool{}
	for _, pos := range shapes(t).Positions {
		if pos.InstrumentType != InstrumentTypeCurrency {
			continue
		}
		assert.Equal(t, "rub", pos.CurrentPrice.Currency,
			"%s is priced in roubles whatever it holds", pos.Ticker)
		code := currencyCodeOf(pos.Ticker)
		require.NotEmpty(t, code, "ticker %q must yield a currency", pos.Ticker)
		seen[code] = true
	}
	assert.Len(t, seen, 3, "three currencies, none of which currentPrice would have named")
	assert.True(t, seen["USD"] && seen["EUR"] && seen["RUB"])
}

// TestBoardsResolveToAMarket: the fallback is only reached for an instrument
// the catalogue does not carry, but when it is reached, every board this broker
// uses for securities has to yield a venue.
func TestBoardsResolveToAMarket(t *testing.T) {
	for _, pos := range shapes(t).Positions {
		if pos.InstrumentType == InstrumentTypeCurrency {
			continue // cash never takes this path
		}
		assert.NotEmpty(t, marketOfClassCode(pos.ClassCode),
			"board %q (%s) resolves to no market", pos.ClassCode, pos.Ticker)
	}
}

// TestInvestBoxIsRecognisable: one token reaches several accounts, and the
// round-up savings pot is one of them. It is left alone by an operator's
// decision, which needs the type to be legible.
func TestInvestBoxIsRecognisable(t *testing.T) {
	var accounts struct {
		Accounts []BrokerAccount `json:"accounts"`
	}
	loadFixture(t, "accounts.json", &accounts)
	require.NotEmpty(t, accounts.Accounts)

	boxes := 0
	for _, a := range accounts.Accounts {
		assert.Equal(t, AccountStatusOpen, a.Status)
		if a.Type == AccountTypeInvestBox {
			boxes++
		}
	}
	assert.Equal(t, 1, boxes)
}

// TestBondsCarryAVenue: bonds joined the catalogue so a bond POSITION has a
// market to take. A bond universe that did not name a venue would make that
// pointless.
func TestBondsCarryAVenue(t *testing.T) {
	var bonds struct {
		Instruments []Instrument `json:"instruments"`
	}
	loadFixture(t, "bonds_shapes.json", &bonds)
	require.NotEmpty(t, bonds.Instruments)
	for _, b := range bonds.Instruments {
		assert.NotEmpty(t, b.FIGI)
		assert.Positive(t, b.Lot, "a lot size is what blockedLots converts through")
		assert.NotEmpty(t, MarketOf(b), "bond %s settles nowhere this system knows", b.Ticker)
	}
}
