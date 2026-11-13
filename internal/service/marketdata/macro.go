package marketdata

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// macroTTL is how long a read is served from memory. These series move at most
// once a day and a widget polls far more often, so an hour-scale TTL turns the
// upstream into a once-per-few-hours call whatever the page traffic.
const macroTTL = 3 * time.Hour

// MacroReader reads the latest print of a series from one publisher.
type MacroReader interface {
	Latest(ctx context.Context, series string) (entity.MacroObservation, error)
}

// MacroSource resolves, for a caller, the publishers it may read, keyed by
// provider slug ("fred", "cbr"). A keyed publisher with no usable account is
// simply absent: the series then reports itself unavailable instead of the
// whole call failing.
type MacroSource interface {
	MacroReadersFor(ctx context.Context, userID string) (map[string]MacroReader, error)
}

type macroSeriesDef struct {
	id, provider, ref, missingReason string
}

// macroSeries is the set of series this service exposes: our id, the
// publisher's provider slug, and the id that publisher knows it by.
//
// The publisher ids live here rather than in the adapters so that adding a
// series is one line next to its siblings; the adapters stay generic readers.
var macroSeries = []macroSeriesDef{
	// DFF is the effective federal funds rate, published daily; the monthly
	// FEDFUNDS would lag the figure a rates widget is expected to show.
	{"fed_funds", "fred", "DFF", "no FRED API key: add a provider account with provider \"fred\""},
	{"cbr_key_rate", "cbr", "key_rate", "Bank of Russia reader is not configured"},
}

// macroCache remembers successful reads. In memory only: a restart costs one
// re-read per series, which does not justify a table.
type macroCache struct {
	mu      sync.Mutex
	entries map[string]macroEntry
	now     func() time.Time
}

type macroEntry struct {
	obs    entity.MacroObservation
	readAt time.Time
}

func newMacroCache() *macroCache {
	return &macroCache{entries: map[string]macroEntry{}, now: time.Now}
}

func (c *macroCache) get(id string) (macroEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || c.now().Sub(e.readAt) >= macroTTL {
		return macroEntry{}, false
	}
	return e, true
}

func (c *macroCache) put(id string, e macroEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = e
}

// WithMacroSource returns a new Handler reading macro series through src.
func (h *Handler) WithMacroSource(src MacroSource) *Handler {
	copied := h.clone()
	copied.macroSource = src
	return copied
}

// GetMacroRates returns the requested macro series, each with the publisher's
// own observation date and the time this instance read it.
//
// Only successful reads are cached. Remembering a failure would keep a series
// dark for hours after the cause is gone, and the rate-limited transport
// already bounds how hard an outage is retried.
//
// shortcut: two concurrent cold calls may both go upstream; one extra read per
// series per TTL is not worth a singleflight. Revisit if a widget polls in
// bursts.
func (h *Handler) GetMacroRates(ctx context.Context, req *connect.Request[apiv1.GetMacroRatesRequest]) (*connect.Response[apiv1.GetMacroRatesResponse], error) {
	wanted, err := wantedMacroSeries(req.Msg.GetSeries())
	if err != nil {
		return nil, err
	}

	var readers map[string]MacroReader
	if h.macroSource != nil {
		userID := ""
		if user, ok := middleware.UserFromContext(ctx); ok {
			userID = user.ID
		}
		if readers, err = h.macroSource.MacroReadersFor(ctx, userID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resolve macro readers: %w", err))
		}
	}

	out := make([]*apiv1.MacroSeries, 0, len(wanted))
	for _, s := range wanted {
		item := &apiv1.MacroSeries{Series: s.id, Source: s.provider}
		reader, ok := readers[s.provider]
		if !ok {
			// Checked before the cache: a caller who cannot read this
			// publisher should not be shown what another caller's key fetched.
			unavailable(item, s.missingReason)
			out = append(out, item)
			continue
		}

		entry, hit := h.macro.get(s.id)
		if !hit {
			obs, err := reader.Latest(ctx, s.ref)
			if err != nil {
				h.log.Warn("macro series read failed", "series", s.id, "error", err)
				unavailable(item, "the publisher could not be read right now")
				out = append(out, item)
				continue
			}
			entry = macroEntry{obs: obs, readAt: h.macro.now()}
			h.macro.put(s.id, entry)
		}

		item.Available = true
		item.Value = entry.obs.Value.String()
		item.ObservationDate = entry.obs.Date.Format(time.DateOnly)
		item.ReadAt = timestamppb.New(entry.readAt)
		out = append(out, item)
	}
	return connect.NewResponse(&apiv1.GetMacroRatesResponse{Series: out}), nil
}

func unavailable(item *apiv1.MacroSeries, reason string) {
	item.Available = false
	item.UnavailableReason = &reason
}

// wantedMacroSeries resolves requested ids to known series, in the order
// asked, once each. Empty means all.
func wantedMacroSeries(ids []string) ([]macroSeriesDef, error) {
	if len(ids) == 0 {
		return macroSeries, nil
	}
	var out []macroSeriesDef
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var found bool
		for _, s := range macroSeries {
			if s.id == id {
				out, found = append(out, s), true
				break
			}
		}
		if !found {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown macro series: "+id))
		}
	}
	return out, nil
}
