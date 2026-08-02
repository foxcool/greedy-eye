// Package marketdepth answers one question about a quote: is there a market
// behind this print, or is it just a number?
//
// It exists because MNEP (Minereum Polygon) stood second in the dev portfolio at
// $4,175 — 300,000 airdropped units times a real CoinGecko price — while its whole
// market turned over $40,655 a day. Nothing was wrong by the system's own rules:
// the token was correctly identified, correctly priced, and correctly summed. The
// lie was in calling that print a valuation.
package marketdepth

import (
	"math"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// MinVolume is the 24h volume, in quote-asset units, below which a print stops
// counting as a market.
//
// Chosen from the dev catalogue measured on 2026-08-02 (personal-6ae.1), not
// guessed: across 66 held assets the volume distribution is bimodal and the
// $100k–1M bucket is EMPTY (7 assets under $1k, 2 under $10k, 5 under $100k,
// then nothing until 27 assets over $1M). Any threshold inside that gap separates
// the same 14 assets; $100k is its lower edge, so it excludes the fewest.
const MinVolume = 100_000

var minVolume = decimal.NewFromInt(MinVolume)

// Thin reports whether the market behind p is too small to treat its price as a
// valuation. rate converts p's own base asset into the quote asset the total is
// expressed in (decimal.NewFromInt(1) on the direct path), because volume is
// denominated in the base the asset actually trades in.
//
// A price with NO reported volume is NOT thin. Absence of evidence is not evidence
// of absence: the 11 no-volume assets on dev are mostly Aave receipt tokens (aUSDC,
// aETHUSDC, aWETH) — real money that has no market of its own by construction, and
// a gate keyed on "volume missing" would report them as unknown.
//
// A reported non-positive volume does count as thin. Nothing writes zero any more
// (coingecko.reported), but rows written before that and providers that never
// promised anything are not trusted to be clean.
func Thin(p *apiv1.Price, rate decimal.Decimal) bool {
	if p == nil || p.Volume == nil {
		return false
	}
	raw, err := decimal.NewFromString(*p.Volume)
	if err != nil {
		// Unparseable is the same claim as unreported: we know nothing about the
		// market, and knowing nothing is not grounds for dropping the holding.
		return false
	}
	volume := raw.Shift(-decI32(p.Decimals)).Mul(rate)
	return volume.LessThan(minVolume)
}

func decI32(d uint32) int32 {
	if d > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(d)
}
