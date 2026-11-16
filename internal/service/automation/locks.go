package automation

import "sync"

// ruleLocks serialises the writes that move one rule's executions: a run
// (which expires the rule's unsigned proposal and creates the next), a cancel,
// and a confirmation. Each reads an execution, decides, and writes it back;
// two at once could both find no waiting proposal and both propose, or one
// could overwrite the other's newer state.
// shortcut: in-process only; a second automation instance needs a Postgres advisory lock keyed by rule id.
type ruleLocks struct {
	mu    sync.Mutex
	rules map[string]*sync.Mutex
}

func newRuleLocks() *ruleLocks { return &ruleLocks{rules: map[string]*sync.Mutex{}} }

// lock takes the rule's lock and returns its release.
func (l *ruleLocks) lock(ruleID string) (unlock func()) {
	l.mu.Lock()
	m, ok := l.rules[ruleID]
	if !ok {
		m = &sync.Mutex{}
		l.rules[ruleID] = m
	}
	l.mu.Unlock()
	m.Lock()
	return m.Unlock
}
