package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/foxcool/greedy-eye/internal/spend"
)

// newsTimeout bounds one news run. Each source has its own fetch bound
// (analytics.feedTimeout); this one stops a run from overlapping the next.
const newsTimeout = 10 * time.Minute

// ingestNews runs on every fire; IngestNews picks the sources that are due.
func (s *Scheduler) ingestNews() {
	ctx, cancel := context.WithTimeout(spend.WithCaller(
		spend.WithClass(context.Background(), spend.ClassBackground), "job:news"), newsTimeout)
	defer cancel()

	start := time.Now()
	report, err := s.news.IngestNews(ctx)
	if err != nil {
		s.log.Error("scheduler: news run failed", slog.Any("error", err))
		return
	}
	// Logged on every failing run, besides being recorded on the source.
	for _, f := range report.Failures {
		s.log.Warn("scheduler: news source failed",
			slog.String("source_id", f.SourceID),
			slog.String("source", f.Name),
			slog.String("error", f.Error))
	}
	if report.Due == 0 {
		return
	}
	s.log.Info("scheduler: news ingested",
		slog.Int("enabled", report.Enabled),
		slog.Int("due", report.Due),
		slog.Int("fetched", report.Fetched),
		slog.Int("failed", report.Failed),
		slog.Int("inserted", report.Inserted),
		slog.Int("skipped", report.Skipped),
		slog.Duration("duration", time.Since(start)))
}
