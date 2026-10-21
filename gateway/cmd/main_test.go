package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
)

func upstream(status int, calls *atomic.Int32, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(delay)
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.WriteHeader(status)
	}))
}

func TestGetIsRetriedOnceOn503(t *testing.T) {
	var calls atomic.Int32
	srv := upstream(503, &calls, 0)
	defer srv.Close()
	rec := httptest.NewRecorder()
	proxy("turnos", srv.URL, "/api/turnos").ServeHTTP(rec, httptest.NewRequest("GET", "/api/turnos/appointments/1", nil))
	if calls.Load() != 2 || rec.Code != 503 {
		t.Fatalf("want 2 calls and final 503, got %d calls, status %d", calls.Load(), rec.Code)
	}
}

func TestPostIsNeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := upstream(503, &calls, 0)
	defer srv.Close()
	rec := httptest.NewRecorder()
	proxy("turnos", srv.URL, "/api/turnos").ServeHTTP(rec, httptest.NewRequest("POST", "/api/turnos/appointments", strings.NewReader("{}")))
	if calls.Load() != 1 {
		t.Fatalf("POST must not be retried, got %d calls", calls.Load())
	}
}

func TestPrefixIsStrippedForTurnos(t *testing.T) {
	var calls atomic.Int32
	srv := upstream(200, &calls, 0)
	defer srv.Close()
	rec := httptest.NewRecorder()
	proxy("turnos", srv.URL, "/api/turnos").ServeHTTP(rec, httptest.NewRequest("GET", "/api/turnos/appointments/9", nil))
	if got := rec.Header().Get("X-Seen-Path"); got != "/appointments/9" {
		t.Fatalf("upstream saw %q", got)
	}
}

func TestCorrelationHeaderIsNotDuplicated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Correlation-ID", r.Header.Get("X-Correlation-ID"))
	}))
	defer srv.Close()
	mux := http.NewServeMux()
	mux.Handle("/api/turnos/", proxy("turnos", srv.URL, "/api/turnos"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/turnos/appointments/1", nil)
	req.Header.Set("X-Correlation-ID", "corr-1")
	obs.Middleware("gateway", mux).ServeHTTP(rec, req)
	if got := rec.Header().Values("X-Correlation-ID"); len(got) != 1 || got[0] != "corr-1" {
		t.Fatalf("want a single corr-1 header, got %v", got)
	}
}

func TestSlowUpstreamReturns504(t *testing.T) {
	var calls atomic.Int32
	srv := upstream(200, &calls, upstreamLimit+500*time.Millisecond)
	defer srv.Close()
	rec := httptest.NewRecorder()
	proxy("turnos", srv.URL, "/api/turnos").ServeHTTP(rec, httptest.NewRequest("POST", "/api/turnos/appointments", strings.NewReader("{}")))
	if rec.Code != 504 || !strings.Contains(rec.Body.String(), "UPSTREAM_TIMEOUT") {
		t.Fatalf("want 504 UPSTREAM_TIMEOUT, got %d %s", rec.Code, rec.Body)
	}
}

func TestUnavailableUpstreamReturns502(t *testing.T) {
	rec := httptest.NewRecorder()
	proxy("notifications", "http://127.0.0.1:1", "").ServeHTTP(rec, httptest.NewRequest("POST", "/v1/notifications", strings.NewReader("{}")))
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "UPSTREAM_UNAVAILABLE") {
		t.Fatalf("want 502, got %d %s", rec.Code, rec.Body)
	}
}
