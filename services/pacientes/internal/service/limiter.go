package service

import (
	"sync"
	"time"
)

// Limiter counts failed logins per email in a fixed window. State lives in
// memory, so the limit applies per instance: with N replicas an attacker gets
// up to N*Max attempts per window (accepted limitation, see README).
type Limiter struct {
	Max    int
	Window time.Duration

	mu      sync.Mutex
	entries map[string]*attempts
}

type attempts struct {
	count int
	start time.Time
}

const maxTrackedKeys = 10000

// Blocked returns how long the key must wait, or 0 when it may try again.
func (l *Limiter) Blocked(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return 0
	}
	end := e.start.Add(l.Window)
	if !now.Before(end) {
		delete(l.entries, key)
		return 0
	}
	if e.count < l.Max {
		return 0
	}
	return end.Sub(now)
}

func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]*attempts{}
	}
	if len(l.entries) >= maxTrackedKeys {
		for k, e := range l.entries {
			if !now.Before(e.start.Add(l.Window)) {
				delete(l.entries, k)
			}
		}
	}
	e, ok := l.entries[key]
	if !ok && len(l.entries) >= maxTrackedKeys {
		return // memory bound: a flood of distinct emails is left to the gateway rate limit
	}
	if !ok || !now.Before(e.start.Add(l.Window)) {
		l.entries[key] = &attempts{count: 1, start: now}
		return
	}
	e.count++
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
