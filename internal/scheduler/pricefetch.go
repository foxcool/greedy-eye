package scheduler

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
)

// fetchPrices pulls fresh prices for all assets from external providers.
// No user in context: the credentials resolver falls back to system-shared
// and env-configured providers.
func (s *Scheduler) fetchPrices() {
	// Background class: on a plan metered by volume, an unattended sweep yields
	// its last fifth of the month's allowance to whoever presses Sync.
	ctx, cancel := context.WithTimeout(
		ratelimit.WithClass(context.Background(), ratelimit.ClassBackground), jobTimeout)
	defer cancel()

	start := time.Now()
	before := s.usageSnapshot()
	resp, err := s.prices.FetchExternalPrices(ctx, connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	if err != nil {
		s.log.Error("scheduler: price fetch failed", slog.Any("error", err))
		return
	}
	if len(resp.Msg.GetErrors()) > 0 {
		s.log.Warn("scheduler: price fetch partial errors",
			slog.Int("error_count", len(resp.Msg.GetErrors())),
			slog.Any("errors", resp.Msg.GetErrors()))
	}

	attrs := []any{
		slog.Int("fetched", int(resp.Msg.GetPricesFetched())),
		slog.Int("stored", int(resp.Msg.GetPricesStored())),
		slog.Duration("duration", time.Since(start)),
	}
	// A run that asked nobody used to log exactly what a run with nothing to
	// ask logs. Naming the idle sources with their reason is what makes those
	// two the different sentences they always were: "everything is current" and
	// "the whole catalogue is postponed" both used to read as fetched=0.
	if idle := resp.Msg.GetIdleSources(); len(idle) > 0 {
		attrs = append(attrs, slog.Any("idle_sources", idle))
	}
	attrs = append(attrs, s.usageDelta(before)...)
	s.log.Info("scheduler: prices fetched", attrs...)
}

// usageSnapshot reads provider spend, or nil when no counter is wired.
func (s *Scheduler) usageSnapshot() map[string]int64 {
	if s.usage == nil {
		return nil
	}
	out := map[string]int64{}
	for _, u := range s.usage.Snapshot() {
		out[u.Provider] += u.Requests
	}
	return out
}

// usageDelta reports what this run cost each provider, and what the month has
// cost so far. There is no metrics system in this process, so the sweep's own
// log line is where the quota arithmetic has to be visible: multiply the delta
// by the runs in a month and compare it with the plan.
func (s *Scheduler) usageDelta(before map[string]int64) []any {
	if before == nil {
		return nil
	}
	var attrs []any
	for provider, total := range s.usageSnapshot() {
		attrs = append(attrs,
			slog.Int64(provider+"_requests", total-before[provider]),
			slog.Int64(provider+"_period_requests", total))
	}
	return attrs
}
