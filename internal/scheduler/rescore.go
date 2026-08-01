package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
)

// rescoreAssets rescores the catalogue for scam-filtering identity verdicts.
// The rescorer logs its own detailed report (counts, flagged assets); here we
// only bound the run and record that it fired.
func (s *Scheduler) rescoreAssets() {
	ctx, cancel := context.WithTimeout(
		ratelimit.WithClass(context.Background(), ratelimit.ClassBackground), jobTimeout)
	defer cancel()

	start := time.Now()
	report, err := s.rescorer.RescoreAssets(ctx)
	if err != nil {
		s.log.Error("scheduler: asset rescore failed", slog.Any("error", err))
		return
	}
	s.log.Info("scheduler: assets rescored",
		slog.Int("scored", report.Scored),
		slog.Int("written", report.Written),
		slog.Int("flagged", len(report.Flagged)),
		slog.Duration("duration", time.Since(start)))
}
