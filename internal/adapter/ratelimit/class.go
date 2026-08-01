package ratelimit

import "context"

// Class marks how a request behaves when the credential's plan volume is
// nearly spent.
//
// The split exists because a monthly allowance is spent by unattended work
// long before a person notices: the sweep runs every hour, a human presses
// Sync a few times a week. Holding a reserve for interactive work means the
// last week of the month still answers the question someone actually asked.
type Class int

const (
	// ClassInteractive is the default: a person is waiting on the answer.
	// It may spend the whole allowance.
	ClassInteractive Class = iota
	// ClassBackground is cron sweeps and catalogue refreshes. It stops at the
	// background reserve, leaving the rest for interactive work.
	ClassBackground
)

type classKey struct{}

// WithClass marks every request made under ctx with a traffic class.
func WithClass(ctx context.Context, c Class) context.Context {
	return context.WithValue(ctx, classKey{}, c)
}

// ClassFromContext reports the class carried by ctx, defaulting to
// interactive: unmarked work is work someone is waiting on.
func ClassFromContext(ctx context.Context) Class {
	if c, ok := ctx.Value(classKey{}).(Class); ok {
		return c
	}
	return ClassInteractive
}
