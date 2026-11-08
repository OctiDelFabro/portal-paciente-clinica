package energyprovider

import (
	"testing"
	"time"
)

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	now := time.Unix(0, 0)
	b := &Breaker{Threshold: 5, OpenFor: 30 * time.Second, Now: func() time.Time { return now }}
	for i := 0; i < 4; i++ {
		if !b.Allow() {
			t.Fatalf("closed breaker rejected call %d", i)
		}
		b.Record(false)
	}
	b.Allow()
	b.Record(true) // a success resets the count
	for i := 0; i < 5; i++ {
		b.Allow()
		b.Record(false)
	}
	if b.State() != StateOpen || b.Allow() {
		t.Fatalf("want open and rejecting, got %v", b.State())
	}
	now = now.Add(29 * time.Second)
	if b.Allow() {
		t.Fatal("open breaker allowed a call before OpenFor")
	}
}

func TestBreakerHalfOpenAllowsOneProbe(t *testing.T) {
	now := time.Unix(0, 0)
	b := &Breaker{Threshold: 1, OpenFor: 30 * time.Second, Now: func() time.Time { return now }}
	b.Allow()
	b.Record(false)
	now = now.Add(30 * time.Second)
	if !b.Allow() || b.State() != StateHalfOpen {
		t.Fatalf("want one probe in half-open, state %v", b.State())
	}
	if b.Allow() {
		t.Fatal("second concurrent probe allowed")
	}
	b.Record(false)
	if b.State() != StateOpen || b.Allow() {
		t.Fatalf("failed probe must reopen, state %v", b.State())
	}
	now = now.Add(30 * time.Second)
	b.Allow()
	b.Record(true)
	if b.State() != StateClosed || !b.Allow() {
		t.Fatalf("successful probe must close, state %v", b.State())
	}
}
