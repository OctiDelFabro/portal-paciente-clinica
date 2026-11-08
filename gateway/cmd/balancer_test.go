package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// instanceServer is a fake Turnos instance whose /healthz status and request
// latency can be changed during the test.
type instanceServer struct {
	*httptest.Server
	calls   atomic.Int32
	health  atomic.Int32
	delayMs atomic.Int32
}

func newInstance(t *testing.T, name string) *instanceServer {
	t.Helper()
	s := &instanceServer{}
	s.health.Store(200)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(int(s.health.Load()))
			return
		}
		s.calls.Add(1)
		time.Sleep(time.Duration(s.delayMs.Load()) * time.Millisecond)
		w.Header().Set("X-Instance", name)
	}))
	t.Cleanup(s.Close)
	return s
}

func deadURL(t *testing.T) string {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

func TestSplitURLsFallsBackToSingleURL(t *testing.T) {
	cases := []struct{ list, single, want string }{
		{"http://a:1, http://b:2 ,", "http://x:9", "http://a:1|http://b:2"},
		{"", "http://x:9", "http://x:9"},
		{"", "", "http://127.0.0.1:8081"},
	}
	for _, c := range cases {
		if got := strings.Join(turnosTargets(c.list, c.single), "|"); got != c.want {
			t.Fatalf("turnosTargets(%q, %q) = %q, want %q", c.list, c.single, got, c.want)
		}
	}
	if _, err := newPool("turnos", []string{"http://ok:1", "not a url"}); err == nil {
		t.Fatal("an invalid instance URL must be rejected")
	}
}

func TestPortalAndPartnerTurnosAreBalancedRoundRobin(t *testing.T) {
	one, two := newInstance(t, "turnos-1"), newInstance(t, "turnos-2")
	cfg := testConfig(t, []string{one.URL, two.URL}, "http://127.0.0.1:1")
	tok := validToken(t, "p-1")
	internal, public := cfg.internalHandler(), cfg.publicHandler()
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		seen[do(internal, "GET", "/api/turnos/appointments", tok).Header().Get("X-Instance")]++
		seen[do(public, "GET", "/v1/doctors", "").Header().Get("X-Instance")]++
	}
	if seen["turnos-1"] != 4 || seen["turnos-2"] != 4 {
		t.Fatalf("want 4/4 distribution, got %v", seen)
	}
	m := metricsText(t, cfg)
	for _, inst := range []string{one.Listener.Addr().String(), two.Listener.Addr().String()} {
		if !strings.Contains(m, `gateway_upstream_requests_total{instance="`+inst+`",upstream="turnos"}`) {
			t.Fatalf("no request counter for %s", inst)
		}
		if !strings.Contains(m, `gateway_upstream_healthy{instance="`+inst+`"} 1`) {
			t.Fatalf("no healthy gauge for %s", inst)
		}
	}
}

func TestHealthCheckNeedsTwoFailuresToMarkDownAndOneOKToRecover(t *testing.T) {
	one, two := newInstance(t, "turnos-1"), newInstance(t, "turnos-2")
	cfg := testConfig(t, []string{one.URL, two.URL}, "http://127.0.0.1:1")
	h, tok := cfg.internalHandler(), validToken(t, "p-1")
	client := &http.Client{Timeout: healthTimeout}
	ctx := context.Background()
	gauge := `gateway_upstream_healthy{instance="` + one.Listener.Addr().String() + `"} `

	one.health.Store(503)
	cfg.turnos.checkOnce(ctx, client)
	if !strings.Contains(metricsText(t, cfg), gauge+"1") {
		t.Fatal("one failed check must not mark the instance down")
	}
	cfg.turnos.checkOnce(ctx, client)
	if !strings.Contains(metricsText(t, cfg), gauge+"0") {
		t.Fatal("two failed checks must mark the instance down")
	}
	before := one.calls.Load()
	for i := 0; i < 4; i++ {
		if rec := do(h, "GET", "/api/turnos/a", tok); rec.Header().Get("X-Instance") != "turnos-2" {
			t.Fatalf("request %d went to %q while turnos-1 is down", i, rec.Header().Get("X-Instance"))
		}
	}
	if one.calls.Load() != before {
		t.Fatal("a down instance received traffic")
	}
	one.health.Store(200)
	cfg.turnos.checkOnce(ctx, client)
	if !strings.Contains(metricsText(t, cfg), gauge+"1") {
		t.Fatal("one OK check must bring the instance back")
	}
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		seen[do(h, "GET", "/api/turnos/a", tok).Header().Get("X-Instance")]++
	}
	if seen["turnos-1"] != 2 || seen["turnos-2"] != 2 {
		t.Fatalf("recovered instance not back in rotation: %v", seen)
	}
}

func TestHealthCheckTimeoutCountsAsFailure(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer slow.Close()
	p, err := newPool("turnos", []string{slow.URL})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 50 * time.Millisecond}
	p.checkOnce(context.Background(), client)
	p.checkOnce(context.Background(), client)
	if p.instances[0].healthy.Load() {
		t.Fatal("two timed-out checks must mark the instance down")
	}
}

func TestWatchRunsChecksPeriodically(t *testing.T) {
	one := newInstance(t, "turnos-1")
	one.health.Store(503)
	p, err := newPool("turnos", []string{one.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.watch(ctx, 20*time.Millisecond, healthTimeout)
	deadline := time.Now().Add(2 * time.Second)
	for p.instances[0].healthy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("watch never marked the failing instance down")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConnectionFailureMarksDownAndPostIsNotRetried(t *testing.T) {
	live := newInstance(t, "turnos-2")
	cfg := testConfig(t, []string{deadURL(t), live.URL}, "http://127.0.0.1:1")
	h, tok := cfg.internalHandler(), validToken(t, "p-1")
	rec := do(h, "POST", "/api/turnos/appointments", tok)
	if rec.Code != 502 || live.calls.Load() != 0 {
		t.Fatalf("POST to a dead instance: status %d, live calls %d (want 502, 0)", rec.Code, live.calls.Load())
	}
	if cfg.turnos.instances[0].healthy.Load() {
		t.Fatal("a connection failure must mark the instance down")
	}
	for i := 0; i < 3; i++ {
		if rec := do(h, "POST", "/api/turnos/appointments", tok); rec.Code != 200 {
			t.Fatalf("after mark-down, POST %d: %d", i, rec.Code)
		}
	}
}

func TestInFlightGetIsRetriedOnAnotherInstance(t *testing.T) {
	live := newInstance(t, "turnos-2")
	cfg := testConfig(t, []string{deadURL(t), live.URL}, "http://127.0.0.1:1")
	rec := do(cfg.publicHandler(), "GET", "/v1/doctors", "")
	if rec.Code != 200 || rec.Header().Get("X-Instance") != "turnos-2" || live.calls.Load() != 1 {
		t.Fatalf("GET: status %d instance %q live calls %d", rec.Code, rec.Header().Get("X-Instance"), live.calls.Load())
	}
}

func TestTimedOutPostIsIndeterminateAndNotRetried(t *testing.T) {
	slow, fast := newInstance(t, "turnos-1"), newInstance(t, "turnos-2")
	slow.delayMs.Store(int32((upstreamLimit + 300*time.Millisecond).Milliseconds()))
	cfg := testConfig(t, []string{slow.URL, fast.URL}, "http://127.0.0.1:1")
	rec := do(cfg.internalHandler(), "POST", "/api/turnos/appointments", validToken(t, "p-1"))
	if rec.Code != 504 || errorCode(t, rec) != "UPSTREAM_TIMEOUT" || fast.calls.Load() != 0 {
		t.Fatalf("want 504 without retry, got %d, fast calls %d", rec.Code, fast.calls.Load())
	}
}

// When every instance is marked down the mark may be stale (checks run every
// 2 s), so the gateway still tries them instead of failing without a call.
func TestAllInstancesDownStillAttempts(t *testing.T) {
	one := newInstance(t, "turnos-1")
	p, err := newPool("turnos", []string{one.URL})
	if err != nil {
		t.Fatal(err)
	}
	p.setHealthy(p.instances[0], false)
	cfg := testConfig(t, []string{one.URL}, "http://127.0.0.1:1")
	cfg.turnos = p
	if rec := do(cfg.publicHandler(), "GET", "/v1/doctors", ""); rec.Code != 200 {
		t.Fatalf("fail-open attempt: %d", rec.Code)
	}
}
