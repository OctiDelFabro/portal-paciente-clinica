package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseOriginsDefaultsToViteDevServer(t *testing.T) {
	if got := strings.Join(parseOrigins(""), ","); got != "http://localhost:5173,http://127.0.0.1:5173" {
		t.Fatalf("default origins = %q", got)
	}
	if got := strings.Join(parseOrigins(" https://a.example , ,https://b.example"), ","); got != "https://a.example,https://b.example" {
		t.Fatalf("parsed origins = %q", got)
	}
}

func TestInternalListenerAnswersPreflightForAllowedOrigin(t *testing.T) {
	s := newStack(t)
	req := httptest.NewRequest("OPTIONS", "/api/turnos/appointments", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	rec := httptest.NewRecorder()
	s.cfg.internalHandler().ServeHTTP(rec, req)
	h := rec.Header()
	if rec.Code != 204 || h.Get("Access-Control-Allow-Origin") != "http://localhost:5173" ||
		!strings.Contains(h.Get("Access-Control-Allow-Headers"), "Authorization") ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("preflight: %d %v", rec.Code, h)
	}
	if s.turnos.calls.Load() != 0 {
		t.Fatal("preflight must not reach the service")
	}
}

func TestCORSHeadersOnlyForAllowedOriginsOnInternalListener(t *testing.T) {
	s := newStack(t)
	cases := []struct {
		name, origin, want string
		public             bool
	}{
		{"allowed origin", "http://127.0.0.1:5173", "http://127.0.0.1:5173", false},
		{"unknown origin", "https://evil.example", "", false},
		{"public listener", "http://localhost:5173", "", true},
	}
	for _, c := range cases {
		h := s.cfg.internalHandler()
		if c.public {
			h = s.cfg.publicHandler()
		}
		rec := do(h, "GET", "/v1/doctors", "", "Origin", c.origin)
		if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != c.want {
			t.Fatalf("%s: %d allow-origin=%q", c.name, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}
