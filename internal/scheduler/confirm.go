package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// confirmTimeout bounds one pass over waiting executions. Each check is a
// read of the venue; a pass that runs out leaves the rest for the next fire.
const confirmTimeout = 2 * time.Minute

// confirmWaiting moves executions that wait on a venue. Quiet when nothing
// moved: most fires find nothing waiting at all.
func (s *Scheduler) confirmWaiting() {
	ctx, cancel := context.WithTimeout(context.Background(), confirmTimeout)
	defer cancel()
	moved, err := s.confirmer.ConfirmWaiting(ctx)
	if err != nil {
		s.log.Error("scheduler: waiting executions not checked", slog.Any("error", err))
		return
	}
	if moved > 0 {
		s.log.Info("scheduler: waiting executions moved", slog.Int("moved", moved))
	}
}
