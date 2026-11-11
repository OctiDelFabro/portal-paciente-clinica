package main

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTokenBucketRefillsAtRateUpToBurst(t *testing.T) {
	l := newTokenBucket(1, 2)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a", now); !ok {
			t.Fatalf("request %d within burst denied", i+1)
		}
	}
	ok, retry := l.Allow("a", now)
	if ok || retry != 1 {
		t.Fatalf("third request: ok=%v retry=%d, want denied with Retry-After 1", ok, retry)
	}
	if ok, _ := l.Allow("b", now); !ok {
		t.Fatal("another key must have its own bucket")
	}
	if ok, _ := l.Allow("a", now.Add(time.Second)); !ok {
		t.Fatal("one token must be refilled after 1 s")
	}
	if ok, _ := l.Allow("a", now.Add(time.Second)); ok {
		t.Fatal("only one token was refilled")
	}
	if ok, _ := l.Allow("a", now.Add(time.Hour)); !ok {
		t.Fatal("bucket must refill after idling")
	}
	if ok, _ := l.Allow("a", now.Add(time.Hour)); !ok {
		t.Fatal("refill is capped at burst (2), second token missing")
	}
	if ok, _ := l.Allow("a", now.Add(time.Hour)); ok {
		t.Fatal("refill must not exceed burst")
	}
}

func TestPortalIsRateLimitedPerSubject(t *testing.T) {
	s := newStack(t)
	s.cfg.limiter = newTokenBucket(0.01, 2)
	h := s.cfg.internalHandler()
	a, b := validToken(t, "p-a"), validToken(t, "p-b")
	for i := 0; i < 2; i++ {
		if rec := do(h, "GET", "/api/turnos/appointments", a); rec.Code != 200 {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	rec := do(h, "GET", "/api/turnos/appointments", a)
	if rec.Code != 429 || errorCode(t, rec) != "RATE_LIMITED" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("want 429 RATE_LIMITED with Retry-After, got %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if s.turnos.calls.Load() != 2 {
		t.Fatalf("limited request reached turnos: %d calls", s.turnos.calls.Load())
	}
	if rec := do(h, "GET", "/api/turnos/appointments", b); rec.Code != 200 {
		t.Fatalf("another subject must not share the bucket: %d", rec.Code)
	}
	if !strings.Contains(metricsText(t, s.cfg), `gateway_rate_limited_total{route="turnos"}`) {
		t.Fatal("gateway_rate_limited_total{route=\"turnos\"} not exported")
	}
}

func TestAnonymousAndInvalidTokenRequestsAreLimitedByIP(t *testing.T) {
	s := newStack(t)
	s.cfg.limiter = newTokenBucket(0.01, 2)
	h := s.cfg.internalHandler()
	from := func(ip, path, token string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = ip + ":5555"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	got := []int{from("10.0.0.1", "/api/medicos/search", ""), from("10.0.0.1", "/api/turnos/x", "bad.token.x"), from("10.0.0.1", "/api/turnos/x", "bad.token.x")}
	if got[0] != 200 || got[1] != 401 || got[2] != 429 {
		t.Fatalf("same IP: got %v, want [200 401 429]", got)
	}
	if code := from("10.0.0.2", "/api/medicos/search", ""); code != 200 {
		t.Fatalf("another IP: %d", code)
	}
}

// The partner capability keeps its own per-consumer limit inside Turnos
// (ADR-008); the portal limiter must not touch /v1, /healthz or /metrics.
func TestRateLimitDoesNotApplyOutsideAPI(t *testing.T) {
	s := newStack(t)
	s.cfg.limiter = newTokenBucket(0.01, 1)
	internal, public := s.cfg.internalHandler(), s.cfg.publicHandler()
	for i := 0; i < 3; i++ {
		for _, p := range []string{"/v1/doctors", "/healthz"} {
			if rec := do(public, "GET", p, ""); rec.Code != 200 {
				t.Fatalf("public %s #%d: %d", p, i, rec.Code)
			}
			if rec := do(internal, "GET", p, ""); rec.Code != 200 {
				t.Fatalf("internal %s #%d: %d", p, i, rec.Code)
			}
		}
		if rec := do(internal, "GET", "/metrics", ""); rec.Code != 200 {
			t.Fatalf("metrics #%d: %d", i, rec.Code)
		}
	}
}

func TestLimiterBoundsActiveKeysAndRecoversAfterIdle(t *testing.T) {
	l := newTokenBucket(10, 20)
	now := time.Unix(1000, 0)
	for i := 0; i < maxBuckets; i++ {
		if ok, _ := l.Allow(strconv.Itoa(i), now); !ok {
			t.Fatal("premature limit")
		}
	}
	if ok, _ := l.Allow("overflow", now); ok || len(l.buckets) != maxBuckets {
		t.Fatalf("limit not applied: %d", len(l.buckets))
	}
	if ok, _ := l.Allow("overflow", now.Add(3*time.Second)); !ok {
		t.Fatal("idle buckets did not recover")
	}
}
