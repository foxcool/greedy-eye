package scheduler

import "log/slog"

// cronLogger adapts slog to the cron.Logger interface used by the
// Recover and SkipIfStillRunning job wrappers.
type cronLogger struct {
	log *slog.Logger
}

func (l *cronLogger) Info(msg string, keysAndValues ...any) {
	l.log.Debug("cron: "+msg, keysAndValues...)
}

func (l *cronLogger) Error(err error, msg string, keysAndValues ...any) {
	l.log.Error("cron: "+msg, append([]any{slog.Any("error", err)}, keysAndValues...)...)
}
