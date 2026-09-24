package state

import "sync"

// Locks allows one operation per workspace at a time. Operations never queue:
// a busy workspace is reported to the caller right away.
type Locks struct {
	mu   sync.Mutex
	held map[string]bool
}

func NewLocks() *Locks { return &Locks{held: map[string]bool{}} }

func (l *Locks) TryLock(p, w string) (unlock func(), ok bool) {
	key := p + "\x00" + w
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return nil, false
	}
	l.held[key] = true
	return func() {
		l.mu.Lock()
		delete(l.held, key)
		l.mu.Unlock()
	}, true
}
