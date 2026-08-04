package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
)

// syncBalances re-reads the balances of accounts that went stale.
//
// The price sweep next door keeps re-pricing whatever quantities the database
// holds, so without this job a portfolio's total updates every hour while the
// amounts under it age indefinitely — measured on prod 2026-08-02, holdings
// carried updated_at from a week earlier.
//
// Background class, like the price job: an unattended sweep must yield the tail
// of a metered plan to whoever presses Sync. The sweep bounds itself by account
// count (see portfolio.SweepOpts) and by this job's timeout; whatever it does
// not reach stays stale and is picked first on the next fire.
func (s *Scheduler) syncBalances() {
	ctx, cancel := context.WithTimeout(
		ratelimit.WithClass(context.Background(), ratelimit.ClassBackground), balanceSyncTimeout)
	defer cancel()

	start := time.Now()
	before := s.usageSnapshot()

	report, err := s.balances.SyncDueAccounts(ctx, s.sweepOpts)
	if err != nil {
		s.log.Error("scheduler: balance sweep failed", slog.Any("error", err))
		return
	}
	s.balances.LogSweepReport(report, time.Since(start))

	if attrs := s.usageDelta(before); len(attrs) > 0 {
		s.log.Info("scheduler: balance sweep provider spend", attrs...)
	}
}
