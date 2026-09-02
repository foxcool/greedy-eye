package marketdata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/scamfilter"
	"github.com/foxcool/greedy-eye/internal/store"
)

// rescorePageSize bounds each catalogue page walked during a rescore.
const rescorePageSize = 200

// rescoreVerdictSource marks verdicts written by the automated pass, so a later
// pass (or a user verdict) can tell machine judgements from human ones.
const rescoreVerdictSource = "heuristic"

// RescoreReport summarizes one rescore pass for the log — the human-readable
// output reviewed before the sync path starts deriving holdings.excluded from
// these verdicts (that derivation lands in A3).
type RescoreReport struct {
	Scored    int            // assets seen
	Written   int            // verdicts actually changed (user verdicts skipped)
	ByVerdict map[string]int // verdict -> count among scored
	// Flagged lists the non-legit assets by descending score, for eyeballing.
	Flagged []FlaggedAsset
}

// FlaggedAsset is one non-legit result in a rescore report.
type FlaggedAsset struct {
	AssetID string
	Symbol  string
	Name    string
	Verdict string
	Score   float64
}

// RescoreAssets walks the whole catalogue, scores each asset's identity with
// scamfilter and persists the verdict, skipping assets a user has judged. It
// logs a report (counts by verdict, the flagged assets by score) so a human can
// review the poisoned catalogue before the verdicts start excluding holdings.
//
// The score uses text signals (symbol/name) plus a cheap listing check: an asset
// no price provider can resolve is a weak scam signal, and the catalogue's
// unpriced tokens are exactly the unlisted scams. Provider signals (moralis
// possible_spam, contract-verified) are not available here — they enter at sync
// intake (A3); this pass judges what the stored catalogue alone reveals.
func (h *Handler) RescoreAssets(ctx context.Context) (RescoreReport, error) {
	weights := scamfilter.DefaultWeights()
	report := RescoreReport{ByVerdict: map[string]int{}}

	pageToken := ""
	for {
		assets, next, err := h.store.ListAssets(ctx, ListAssetsOpts{
			PageSize:  rescorePageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return report, fmt.Errorf("list assets for rescore: %w", err)
		}

		for _, a := range assets {
			// A user verdict is terminal — do not even rescore it.
			if isUserVerdict(a.VerdictSource) {
				continue
			}

			result := scamfilter.Score(scamfilter.Input{
				Symbol:          a.Symbol,
				Name:            a.Name,
				HasPriceListing: h.hasPriceListing(ctx, a.ID),
				// The catalogue changes under an asset: a ticker unclaimed when
				// the asset was first seen may be held by the time the real one
				// is listed, so the collision is re-asked here rather than
				// frozen at intake.
				ClaimsHeldTicker: h.claimsHeldTicker(ctx, a.ID),
				// The venue is read from the stored row, so a catalogue poisoned
				// by the old rule repairs itself on the next pass rather than
				// waiting for each instrument to be synced again.
				VenueListed: entity.IsListedVenue(a.Market),
			}, weights)

			report.Scored++
			report.ByVerdict[string(result.Verdict)]++

			score := result.Score
			written, err := h.store.SetAssetVerdict(
				ctx, a.ID, string(result.Verdict), &score, result.Signals, rescoreVerdictSource)
			if err != nil {
				return report, fmt.Errorf("set verdict for asset %s: %w", a.ID, err)
			}
			if written {
				report.Written++
			}
			if result.Verdict != scamfilter.VerdictLegit && result.Verdict != scamfilter.VerdictUnknown {
				report.Flagged = append(report.Flagged, FlaggedAsset{
					AssetID: a.ID, Symbol: a.Symbol, Name: a.Name,
					Verdict: string(result.Verdict), Score: result.Score,
				})
			}
		}

		if next == "" {
			break
		}
		pageToken = next
	}

	sort.Slice(report.Flagged, func(i, j int) bool {
		return report.Flagged[i].Score > report.Flagged[j].Score
	})
	h.logRescoreReport(report)
	return report, nil
}

// hasPriceListing reports whether any price provider has resolved this asset,
// returning nil (unknown, does not contribute) on a lookup error rather than a
// spurious false. An empty base/source means "any".
func (h *Handler) hasPriceListing(ctx context.Context, assetID string) *bool {
	_, err := h.store.GetLatestPrice(ctx, assetID, "", "")
	if err == nil {
		yes := true
		return &yes
	}
	if errors.Is(err, store.ErrNotFound) {
		no := false
		return &no
	}
	return nil
}

// logRescoreReport emits the pass summary and the flagged assets. The flagged
// list is capped so a badly poisoned catalogue does not flood the log; the
// counts still reflect the whole catalogue.
func (h *Handler) logRescoreReport(r RescoreReport) {
	if h.log == nil {
		return
	}
	h.log.Info("asset rescore complete",
		slog.Int("scored", r.Scored),
		slog.Int("written", r.Written),
		slog.Int("scam", r.ByVerdict[string(scamfilter.VerdictScam)]),
		slog.Int("impersonation", r.ByVerdict[string(scamfilter.VerdictImpersonation)]),
		slog.Int("suspect", r.ByVerdict[string(scamfilter.VerdictSuspect)]),
		slog.Int("legit", r.ByVerdict[string(scamfilter.VerdictLegit)]),
	)

	const maxLogged = 50
	for i, f := range r.Flagged {
		if i >= maxLogged {
			h.log.Info("asset rescore: flagged list truncated",
				slog.Int("shown", maxLogged), slog.Int("total", len(r.Flagged)))
			break
		}
		h.log.Info("asset rescore: flagged",
			slog.String("verdict", f.Verdict),
			slog.Float64("score", f.Score),
			slog.String("symbol", f.Symbol),
			slog.String("name", f.Name),
			slog.String("asset_id", f.AssetID),
		)
	}
}

// isUserVerdict reports whether a verdict source is a terminal human judgement.
func isUserVerdict(source string) bool {
	return len(source) >= 5 && source[:5] == "user:"
}
