package scheduler

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// fetchPrices pulls fresh prices for all assets from external providers.
// No user in context: the credentials resolver falls back to system-shared
// and env-configured providers.
func (s *Scheduler) fetchPrices() {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()

	start := time.Now()
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
	s.log.Info("scheduler: prices fetched",
		slog.Int("fetched", int(resp.Msg.GetPricesFetched())),
		slog.Int("stored", int(resp.Msg.GetPricesStored())),
		slog.Duration("duration", time.Since(start)))
}
