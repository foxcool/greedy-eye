//go:build eval

package scamfilter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The eval measures the scorer against a hand-labelled sample of the real
// catalogue. The unit tests pin known shapes; this answers how often the
// verdict is right on what actually arrives. Run it with:
//
//	go test -tags eval -run TestEval -v ./internal/scamfilter/
//
// The golden set is drawn from the owner's own catalogue and is never
// committed: the set of spam tokens a wallet received identifies the wallet.
// Without the file the eval skips.
const goldenPath = "testdata/identity_golden.jsonl"

// goldenRow is one labelled asset. The context fields are reconstructed from
// the signals the snapshot stored, so they are exact only as far as the scorer
// asked them: has_price_listing is false where no_listing fired and unknown
// (nil, never contributes) otherwise.
//
// The sample is stratified by the snapshot verdict, so each row carries the
// inverse of its stratum's sampling rate as Weight. Rates are what turn the
// sample back into the catalogue: 20 legit rows stand for 257 assets, 10
// impersonation rows for 24, and an unweighted count would overstate the
// rare strata tenfold.
type goldenRow struct {
	ID               string  `json:"id"`
	Symbol           string  `json:"symbol"`
	Name             string  `json:"name"`
	VenueListed      bool    `json:"venue_listed"`
	HasPriceListing  *bool   `json:"has_price_listing"`
	ClaimsHeldTicker bool    `json:"claims_held_ticker"`
	SnapshotVerdict  Verdict `json:"snapshot_verdict"`
	Weight           float64 `json:"weight"`
	// Label is the ground truth: scam (not the owner's money), legit (a real
	// asset, however small or dead) or unsure (left out of the metrics).
	Label       string `json:"label"`
	LabelSource string `json:"label_source"`
}

func TestEval_IdentityGolden(t *testing.T) {
	rows := loadGolden(t)

	var (
		cells      confusion
		raw        confusion
		unsure     int
		drift      []string
		errorsSeen []string
	)
	for _, r := range rows {
		got := Score(Input{
			Symbol:           r.Symbol,
			Name:             r.Name,
			HasPriceListing:  r.HasPriceListing,
			ClaimsHeldTicker: r.ClaimsHeldTicker,
			VenueListed:      r.VenueListed,
		}, DefaultWeights()).Verdict

		// Under the weights that produced the snapshot a mismatch means the
		// reconstruction lost an input; under tuned weights it is the change
		// being measured. Either way it is worth seeing row by row.
		if got != r.SnapshotVerdict {
			drift = append(drift, fmt.Sprintf("%s → %s  %q", r.SnapshotVerdict, got, r.Symbol))
		}

		if r.Label == "unsure" {
			unsure++
			continue
		}
		truthScam := r.Label == "scam"
		excluded := quarantines(got)
		cells.add(truthScam, excluded, r.Weight)
		raw.add(truthScam, excluded, 1)
		if truthScam != excluded {
			kind := "FN leaks into sums"
			if excluded {
				kind = "FP hides real money"
			}
			errorsSeen = append(errorsSeen, fmt.Sprintf("%-19s %-13s %-13s %q", kind, got, r.LabelSource, r.Symbol))
		}
	}

	t.Logf("rows %d, scored %d, unsure %d", len(rows), len(rows)-unsure, unsure)
	t.Logf("weighted to the catalogue (%.0f assets):", cells.total())
	t.Logf("                 excluded  counted")
	t.Logf("  truth scam     %8.0f %8.0f", cells.tp, cells.fn)
	t.Logf("  truth legit    %8.0f %8.0f", cells.fp, cells.tn)
	t.Logf("precision %.3f  recall %.3f  (weighted)", cells.precision(), cells.recall())

	pLo, pHi := wilson(int(raw.tp), int(raw.tp+raw.fp))
	rLo, rHi := wilson(int(raw.tp), int(raw.tp+raw.fn))
	t.Logf("precision %.0f/%.0f [%.2f, %.2f]  recall %.0f/%.0f [%.2f, %.2f]  (sample, Wilson 95%%)",
		raw.tp, raw.tp+raw.fp, pLo, pHi, raw.tp, raw.tp+raw.fn, rLo, rHi)

	sort.Strings(errorsSeen)
	for _, e := range errorsSeen {
		t.Logf("  %s", e)
	}
	t.Logf("verdicts changed vs snapshot: %d", len(drift))
	for _, d := range drift {
		t.Logf("  %s", d)
	}
}

// quarantines mirrors portfolio.isQuarantineVerdict: the decision with a
// consequence is whether a holding leaves the sums, and suspect does not.
// Measuring the verdict label instead would score a suspect airdrop lure as a
// catch while it is still being counted as money.
func quarantines(v Verdict) bool {
	return v == VerdictScam || v == VerdictImpersonation
}

// confusion holds the four outcomes with scam as the positive class.
type confusion struct{ tp, fp, fn, tn float64 }

func (c *confusion) add(truthScam, excluded bool, w float64) {
	switch {
	case truthScam && excluded:
		c.tp += w
	case truthScam:
		c.fn += w
	case excluded:
		c.fp += w
	default:
		c.tn += w
	}
}

func (c confusion) total() float64     { return c.tp + c.fp + c.fn + c.tn }
func (c confusion) precision() float64 { return ratio(c.tp, c.tp+c.fp) }
func (c confusion) recall() float64    { return ratio(c.tp, c.tp+c.fn) }

func ratio(a, b float64) float64 {
	if b == 0 {
		return math.NaN()
	}
	return a / b
}

// wilson is the 95% score interval for k successes in n trials. At n in the
// tens a point estimate alone reads as more certain than it is; the interval
// is the honest size of what the sample can say. It ignores the stratum
// weights, so it is a guide to the sample's resolution, not an exact interval
// for the weighted figure.
func wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return math.NaN(), math.NaN()
	}
	const z = 1.96
	p, nf := float64(k)/float64(n), float64(n)
	den := 1 + z*z/nf
	mid := p + z*z/(2*nf)
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf))
	return (mid - half) / den, (mid + half) / den
}

func loadGolden(t *testing.T) []goldenRow {
	t.Helper()
	data, err := os.ReadFile(goldenPath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no golden set at %s: it is built from the owner catalogue and kept out of the repository", goldenPath)
	}
	require.NoError(t, err)

	var rows []goldenRow
	for i, text := range strings.Split(string(data), "\n") {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		var r goldenRow
		require.NoError(t, json.Unmarshal([]byte(text), &r), "golden line %d", i+1)
		require.Contains(t, []string{"scam", "legit", "unsure"}, r.Label, "golden line %d", i+1)
		require.Positive(t, r.Weight, "golden line %d", i+1)
		rows = append(rows, r)
	}
	require.NotEmpty(t, rows)
	return rows
}
