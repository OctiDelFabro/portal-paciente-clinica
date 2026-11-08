package energyprovider

import (
	"sync"
	"time"
)

// State of the circuit breaker; the value is exported as provider_circuit_state.
type State int

const (
	StateClosed   State = 0
	StateHalfOpen State = 1
	StateOpen     State = 2
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	}
	return "closed"
}

// Breaker opens after Threshold consecutive failures and rejects calls for
// OpenFor; then it lets one probe through (half-open): success closes it,
// failure opens it again. It keeps a dead provider from adding its whole
// budget to every booking (ADR-010).
type Breaker struct {
	Threshold int
	OpenFor   time.Duration
	Now       func() time.Time

	mu       sync.Mutex
	state    State
	failures int
	openedAt time.Time
	probing  bool
}

// NewBreaker uses the same policy as the e-mail provider: 5 failures, 30 s.
func NewBreaker() *Breaker { return &Breaker{Threshold: 5, OpenFor: 30 * time.Second, Now: time.Now} }

// Allow reports whether a call may go to the provider. Every allowed call
// must be followed by exactly one Record.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateOpen:
		if b.Now().Sub(b.openedAt) < b.OpenFor {
			return false
		}
		b.state, b.probing = StateHalfOpen, true
		return true
	case StateHalfOpen:
		if b.probing {
			return false
		}
		b.probing = true
		return true
	}
	return true
}

// Record reports the outcome of an allowed call.
func (b *Breaker) Record(ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	if ok {
		b.state, b.failures = StateClosed, 0
		return
	}
	b.failures++
	if b.state == StateHalfOpen || b.failures >= b.Threshold {
		b.state, b.openedAt = StateOpen, b.Now()
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
