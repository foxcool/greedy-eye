package marketdata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// DatedPriceProvider is implemented by providers whose source publishes one
// rate set per day and can be asked for a past one.
type DatedPriceProvider interface {
	// FetchPricesOn returns the rows of the set in force on day, in the shape
	// FetchPrices writes. Rows are dated by the set, not by day, so several
	// days can come back with the same rows.
	FetchPricesOn(ctx context.Context, assets []*entity.Asset, day time.Time) ([]entity.StoredPrice, error)
}

// cbrSourceID is the source BackfillCbrRates reads. Named here rather than
// imported, like coingeckoProvider: the handler knows
// providers by slug, never by adapter.
const cbrSourceID = "cbr"

const (
	// maxBackfillDays bounds one call. Each day is one request to the feed,
	// and a range typed a decade too wide should be refused, not served.
	maxBackfillDays = 3700

	// heldLookback reaches back from the first requested day for rows already
	// held: a day with no set of its own is answered with an earlier one, so a
	// Monday's rows are dated the Saturday before.
	heldLookback = 14 * 24 * time.Hour

	// backfillPageSize is the page used to read the catalogue and the held
	// history.
	backfillPageSize = 500
)

// heldRow identifies a price by what makes it unique for one source and pair.
type heldRow struct {
	assetID string
	at      int64 // unix seconds of the row's timestamp
}

// BackfillCbrRates writes the Bank of Russia set in force on each day of the
// range as ordinary cbr rows. Admin-only, like every price write (see
// CreatePrice).
//
// A row already held for (asset, set date) is never written again, whoever
// wrote it — the sweep or an earlier backfill — so re-running a range creates
// nothing. The check is made before writing rather than left to the store,
// because the store reports a held row as stored and the operator's question
// is how many rows this call added.
func (h *Handler) BackfillCbrRates(ctx context.Context, req *connect.Request[apiv1.BackfillCbrRatesRequest]) (*connect.Response[apiv1.BackfillCbrRatesResponse], error) {
	if _, err := requireAdmin(ctx, "backfilling rates"); err != nil {
		return nil, err
	}
	from, to, err := backfillRange(req.Msg.GetFromDate(), req.Msg.GetToDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	providers, err := h.resolveProviders(ctx)
	if err != nil && connect.CodeOf(err) != connect.CodeUnimplemented {
		return nil, err
	}
	var (
		provider PriceProvider
		dated    DatedPriceProvider
	)
	for _, p := range providers[cbrSourceID] {
		if d, ok := p.(DatedPriceProvider); ok {
			provider, dated = p, d
			break
		}
	}
	if dated == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("no %s price provider that can read a past day is configured", cbrSourceID))
	}

	assets, err := h.backfillTargets(ctx, provider)
	if err != nil {
		return nil, toConnectError(err)
	}
	if len(assets) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the catalogue holds no asset the %s feed prices", cbrSourceID))
	}

	baseID, err := h.backfillBase(ctx, provider)
	if err != nil {
		return nil, err
	}

	held, err := h.heldRows(ctx, assets, baseID, from.Add(-heldLookback), to.Add(24*time.Hour))
	if err != nil {
		return nil, toConnectError(err)
	}

	resp := &apiv1.BackfillCbrRatesResponse{}
	seen := map[heldRow]bool{}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if err := ctx.Err(); err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("stopped before %s: %v", day.Format(time.DateOnly), err))
			break
		}
		resp.DaysRequested++

		rows, err := dated.FetchPricesOn(ctx, assets, day)
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", day.Format(time.DateOnly), err))
			continue
		}

		var fresh []*entity.StoredPrice
		for i := range rows {
			key := heldRow{assetID: rows[i].AssetID, at: rows[i].Timestamp.Unix()}
			if seen[key] {
				// Another day of the range was answered with the same set.
				continue
			}
			seen[key] = true
			if held[key] {
				resp.PricesHeld++
				continue
			}
			rows[i].BaseAssetID = baseID
			fresh = append(fresh, &rows[i])
		}
		if len(fresh) == 0 {
			continue
		}

		// Written per day, so a failure later in the range keeps what came
		// before it; a re-run then only fills the gap.
		n, err := h.store.CreatePrices(ctx, fresh)
		resp.PricesCreated += uint32(n) // #nosec G115 -- rows of one day's set, bounded by the feed
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", day.Format(time.DateOnly), err))
		}
	}

	h.log.Info("cbr rates backfilled",
		"from", from.Format(time.DateOnly), "to", to.Format(time.DateOnly),
		"days", resp.DaysRequested, "created", resp.PricesCreated, "held", resp.PricesHeld,
		"errors", len(resp.Errors))
	return connect.NewResponse(resp), nil
}

// backfillRange parses the inclusive range as calendar days.
func backfillRange(fromDate, toDate string) (from, to time.Time, err error) {
	if fromDate == "" || toDate == "" {
		return from, to, errors.New("from_date and to_date are required")
	}
	if from, err = time.Parse(time.DateOnly, fromDate); err != nil {
		return from, to, fmt.Errorf("from_date: want YYYY-MM-DD: %w", err)
	}
	if to, err = time.Parse(time.DateOnly, toDate); err != nil {
		return from, to, fmt.Errorf("to_date: want YYYY-MM-DD: %w", err)
	}
	if to.Before(from) {
		return from, to, errors.New("to_date is before from_date")
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > maxBackfillDays {
		return from, to, fmt.Errorf("range covers %d days, at most %d per call", days, maxBackfillDays)
	}
	return from, to, nil
}

// backfillTargets reads the catalogue for what the provider would price: the
// assets it asks about, on a market its binding admits, uncontested and not
// quarantined — the same gates the sweep applies, so history is written only
// for assets the sweep would keep current.
//
// The whole catalogue is paged rather than selected by symbol: the store
// offers no type filter, and this runs once per instance.
func (h *Handler) backfillTargets(ctx context.Context, p PriceProvider) ([]*entity.Asset, error) {
	var all []*entity.Asset
	token := ""
	for {
		page, next, err := h.store.ListAssets(ctx, ListAssetsOpts{PageSize: backfillPageSize, PageToken: token})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" || len(page) == 0 {
			break
		}
		token = next
	}

	all = slices.DeleteFunc(askedOf(p, all), func(a *entity.Asset) bool {
		return slices.Contains(quarantineVerdicts, a.IdentityVerdict)
	})
	admitted, _, err := h.admitted(ctx, p.Binding(), all, map[string]bool{})
	return admitted, err
}

// backfillBase resolves the provider's quote currency the way the sweep does,
// so backfilled rows and swept ones share one pair.
func (h *Handler) backfillBase(ctx context.Context, p PriceProvider) (string, error) {
	symbol, typ := p.BaseAssetSymbol(), p.BaseAssetType()
	if err := quotableIdentity(symbol, entity.DefaultMarket(typ), typ); err != nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("base asset %s: %w", symbol, err))
	}
	base, err := h.store.GetOrCreateAssetBySymbol(ctx, symbol, symbol, typ)
	if err != nil {
		return "", toConnectError(err)
	}
	if err := quotableBase(base); err != nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("base asset %s: %w", symbol, err))
	}
	return base.ID, nil
}

// heldRows reads which (asset, timestamp) rows this source already holds
// against base within [from, to].
func (h *Handler) heldRows(ctx context.Context, assets []*entity.Asset, baseID string, from, to time.Time) (map[heldRow]bool, error) {
	held := map[heldRow]bool{}
	for _, a := range assets {
		token := ""
		for {
			page, next, err := h.store.ListPriceHistory(ctx, ListPriceHistoryOpts{
				AssetID: a.ID, BaseAssetID: baseID, SourceID: cbrSourceID,
				From: &from, To: &to, PageSize: backfillPageSize, PageToken: token,
			})
			if err != nil {
				return nil, fmt.Errorf("read held %s rows of %s: %w", cbrSourceID, a.Symbol, err)
			}
			for _, p := range page {
				held[heldRow{assetID: p.AssetID, at: p.Timestamp.Unix()}] = true
			}
			if next == "" || len(page) == 0 {
				break
			}
			token = next
		}
	}
	return held, nil
}
