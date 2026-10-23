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

// La capacidad publicada de Turnos y la de Notificaciones comparten /v1 en el
// gateway; cada ruta debe llegar a su servicio sin cambiar el path.
func TestPublicV1RoutesReachTheirService(t *testing.T) {
	var turnosCalls, notifCalls atomic.Int32
	turnos := upstream(200, &turnosCalls, 0)
	defer turnos.Close()
	notif := upstream(200, &notifCalls, 0)
	defer notif.Close()
	mux := routes(turnos.URL, notif.URL)
	cases := []struct {
		method, path string
		toTurnos     bool
	}{
		{"POST", "/v1/appointments", true},
		{"GET", "/v1/appointments/a1", true},
		{"POST", "/v1/appointments/a1/cancel", true},
		{"GET", "/v1/doctors", true},
		{"GET", "/v1/usage", true},
		{"GET", "/v1/templates", false},
		{"POST", "/v1/notifications", false},
		{"GET", "/v1/notifications/n1", false},
	}
	for _, c := range cases {
		beforeT, beforeN := turnosCalls.Load(), notifCalls.Load()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		hitTurnos := turnosCalls.Load() == beforeT+1 && notifCalls.Load() == beforeN
		hitNotif := notifCalls.Load() == beforeN+1 && turnosCalls.Load() == beforeT
		if (c.toTurnos && !hitTurnos) || (!c.toTurnos && !hitNotif) || rec.Header().Get("X-Seen-Path") != c.path {
			t.Fatalf("%s %s: turnos=%v notif=%v path=%q", c.method, c.path, hitTurnos, hitNotif, rec.Header().Get("X-Seen-Path"))
		}
	}
}
