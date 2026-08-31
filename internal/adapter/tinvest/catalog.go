package tinvest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// catalogTTL is how long the instrument universe is reused. The universe
// changes when paper is listed or delisted, which is a daily event at most, and
// a stale entry costs a price rather than a wrong one — the price call names the
// FIGI and the exchange answers for it.
const catalogTTL = 12 * time.Hour

// catalog answers "which instrument is this ticker on this venue?" from a cached
// snapshot of the broker's tradable universe.
//
// Two calls fetch it whole (shares and funds), which is why it is a snapshot
// rather than a per-asset lookup: FindInstrument would be one request per asset
// per sweep, and the sweep exists precisely to bound what a provider is asked
// for.
type catalog struct {
	client *Client
	ttl    time.Duration

	mu       sync.Mutex
	loadedAt time.Time
	// byFIGI holds every instrument in the universe.
	byFIGI map[string]Instrument
	// byTicker groups instruments sharing a ticker. A slice, not a single
	// value: the same ticker exists on several venues, and collapsing them is
	// exactly the silent pick this adapter must not make.
	byTicker map[string][]Instrument
}

func newCatalog(c *Client) *catalog {
	return &catalog{client: c, ttl: catalogTTL}
}

// instrument returns the instrument behind a FIGI. found is false when the
// universe does not carry it, which is the honest answer for delisted paper.
func (c *catalog) instrument(ctx context.Context, figi string) (Instrument, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ctx); err != nil {
		return Instrument{}, false, err
	}
	inst, ok := c.byFIGI[strings.TrimSpace(figi)]
	return inst, ok, nil
}

// match resolves a ticker on one venue to exactly one instrument.
//
// exchanges scopes the search to the settlement venues the caller means, because
// a ticker is only unique inside one — SBER exists on MOEX and a US ticker can
// exist on both SPB and a dealer desk. A binding is made only when exactly one
// instrument answers: an ambiguous ticker is not a binding, and picking from two
// candidates silently is what personal-avm exists to prevent. The count comes
// back so the caller can say which of the two cases it hit.
func (c *catalog) match(ctx context.Context, ticker string, exchanges []string) (inst Instrument, candidates int, err error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return Instrument{}, 0, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ctx); err != nil {
		return Instrument{}, 0, err
	}

	var found []Instrument
	for _, candidate := range c.byTicker[ticker] {
		if onVenue(candidate.RealExchange, exchanges) {
			found = append(found, candidate)
		}
	}
	if len(found) != 1 {
		return Instrument{}, len(found), nil
	}
	return found[0], 1, nil
}

// ensure loads the universe when it is missing or stale. The caller holds the
// lock, and it is held across the fetch on purpose: concurrent sweeps would
// otherwise each download the same universe to build the same map.
func (c *catalog) ensure(ctx context.Context) error {
	if c.byFIGI != nil && time.Since(c.loadedAt) <= c.ttl {
		return nil
	}

	shares, err := c.client.Shares(ctx)
	if err != nil {
		return fmt.Errorf("tinvest instrument catalog: %w", err)
	}
	etfs, err := c.client.Etfs(ctx)
	if err != nil {
		return fmt.Errorf("tinvest instrument catalog: %w", err)
	}
	// Bonds join the universe because a bond POSITION needs a venue to take its
	// market from, and the portfolio response carries no venue. Prices are a
	// separate question: FetchPrices still speaks only for what it can quote.
	bonds, err := c.client.Bonds(ctx)
	if err != nil {
		return fmt.Errorf("tinvest instrument catalog: %w", err)
	}

	total := len(shares) + len(etfs) + len(bonds)
	byFIGI := make(map[string]Instrument, total)
	byTicker := make(map[string][]Instrument, total)
	all := make([]Instrument, 0, total)
	all = append(all, shares...)
	all = append(all, etfs...)
	all = append(all, bonds...)
	for _, inst := range all {
		if inst.FIGI == "" {
			continue
		}
		byFIGI[inst.FIGI] = inst
		key := strings.ToUpper(strings.TrimSpace(inst.Ticker))
		if key == "" {
			continue
		}
		byTicker[key] = append(byTicker[key], inst)
	}

	c.byFIGI = byFIGI
	c.byTicker = byTicker
	c.loadedAt = time.Now()
	return nil
}

func onVenue(realExchange string, allowed []string) bool {
	for _, want := range allowed {
		if strings.EqualFold(strings.TrimSpace(realExchange), want) {
			return true
		}
	}
	return false
}
