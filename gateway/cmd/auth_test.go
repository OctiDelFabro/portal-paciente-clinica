package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func validClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{"iss": "pacientes", "sub": "p-1", "role": "patient", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
}

func signHS(t *testing.T, method jwt.SigningMethod, claims jwt.MapClaims, secret string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validToken(t *testing.T, sub string) string {
	c := validClaims()
	c["sub"] = sub
	return signHS(t, jwt.SigningMethodHS256, c, testSecret)
}

// recorder is a fake upstream that records what reached it.
type recorder struct {
	*httptest.Server
	calls   atomic.Int32
	path    atomic.Value
	patient atomic.Value
	role    atomic.Value
}

func newRecorder(t *testing.T, instance string) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			return
		}
		rec.calls.Add(1)
		rec.path.Store(r.URL.Path)
		rec.patient.Store(strings.Join(r.Header.Values("X-Patient-Id"), ","))
		rec.role.Store(strings.Join(r.Header.Values("X-User-Role"), ","))
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.Header().Set("X-Instance", instance)
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (r *recorder) seen(v *atomic.Value) string {
	s, _ := v.Load().(string)
	return s
}

type stack struct {
	turnos, notif, pacientes, medicos, historia *recorder
	cfg                                         config
}

func newStack(t *testing.T) *stack {
	t.Helper()
	s := &stack{turnos: newRecorder(t, "turnos-1"), notif: newRecorder(t, "notif"), pacientes: newRecorder(t, "pac"),
		medicos: newRecorder(t, "med"), historia: newRecorder(t, "his")}
	s.cfg = testConfig(t, []string{s.turnos.URL}, s.notif.URL)
	s.cfg.pacientes, s.cfg.medicos, s.cfg.historia = s.pacientes.URL, s.medicos.URL, s.historia.URL
	return s
}

func testConfig(t *testing.T, turnosURLs []string, notifURL string) config {
	t.Helper()
	auth, err := newAuthenticator(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPool("turnos", turnosURLs)
	if err != nil {
		t.Fatal(err)
	}
	return config{turnos: p, notifications: notifURL, pacientes: "http://127.0.0.1:1", medicos: "http://127.0.0.1:1",
		historia: "http://127.0.0.1:1", auth: auth, limiter: newTokenBucket(1000, 1000), corsOrigins: defaultCORSOrigins}
}

func do(h http.Handler, method, path, token string, headers ...string) *httptest.ResponseRecorder {
	var body io.Reader
	if method != "GET" {
		body = strings.NewReader("{}")
	}
	req := httptest.NewRequest(method, path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}
	return body.Error.Code
}

func TestAuthenticatorRequiresSecretOfAtLeast32Bytes(t *testing.T) {
	if _, err := newAuthenticator(strings.Repeat("x", 31)); err == nil {
		t.Fatal("31-byte secret must be rejected")
	}
	if _, err := newAuthenticator(""); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	if _, err := newAuthenticator(strings.Repeat("x", 32)); err != nil {
		t.Fatalf("32-byte secret must be accepted: %v", err)
	}
}

func TestProtectedRouteRejectsInvalidTokens(t *testing.T) {
	expired := validClaims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	noExp := validClaims()
	delete(noExp, "exp")
	wrongIss := validClaims()
	wrongIss["iss"] = "otro"
	noSub := validClaims()
	delete(noSub, "sub")
	noRole := validClaims()
	delete(noRole, "role")
	badSub := validClaims()
	badSub["sub"] = "p-1\r\nX-User-Role: admin"
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, header string }{
		{"missing header", ""},
		{"not bearer", "Basic " + validToken(t, "p-1")},
		{"malformed", "Bearer abc.def.ghi"},
		{"alg none", "Bearer " + none},
		{"wrong alg HS512", "Bearer " + signHS(t, jwt.SigningMethodHS512, validClaims(), testSecret)},
		{"wrong secret", "Bearer " + signHS(t, jwt.SigningMethodHS256, validClaims(), strings.Repeat("z", 32))},
		{"expired", "Bearer " + signHS(t, jwt.SigningMethodHS256, expired, testSecret)},
		{"no exp", "Bearer " + signHS(t, jwt.SigningMethodHS256, noExp, testSecret)},
		{"wrong iss", "Bearer " + signHS(t, jwt.SigningMethodHS256, wrongIss, testSecret)},
		{"no sub", "Bearer " + signHS(t, jwt.SigningMethodHS256, noSub, testSecret)},
		{"no role", "Bearer " + signHS(t, jwt.SigningMethodHS256, noRole, testSecret)},
		{"unsafe sub", "Bearer " + signHS(t, jwt.SigningMethodHS256, badSub, testSecret)},
	}
	s := newStack(t)
	h := s.cfg.internalHandler()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/turnos/appointments", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("want 401 UNAUTHORIZED JSON, got %d %s", rec.Code, rec.Body)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 must carry WWW-Authenticate")
			}
		})
	}
	if s.turnos.calls.Load() != 0 {
		t.Fatalf("invalid tokens reached turnos %d times", s.turnos.calls.Load())
	}
}

func TestValidTokenForwardsIdentityHeaders(t *testing.T) {
	s := newStack(t)
	rec := do(s.cfg.internalHandler(), "GET", "/api/turnos/appointments", validToken(t, "p-42"))
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if got := s.turnos.seen(&s.turnos.patient); got != "p-42" {
		t.Fatalf("X-Patient-Id = %q", got)
	}
	if got := s.turnos.seen(&s.turnos.role); got != "patient" {
		t.Fatalf("X-User-Role = %q", got)
	}
}

// Identity headers sent by the client are never trusted, on any route of
// either listener; only the gateway sets them, from a verified token.
func TestClientIdentityHeadersAreAlwaysStripped(t *testing.T) {
	s := newStack(t)
	spoof := []string{"X-Patient-Id", "p-evil", "x-user-role", "admin"}
	internal, public := s.cfg.internalHandler(), s.cfg.publicHandler()
	cases := []struct {
		name         string
		h            http.Handler
		method, path string
		token        string
		up           *recorder
		wantP, wantR string
	}{
		{"internal open login", internal, "POST", "/api/pacientes/auth/login", "", s.pacientes, "", ""},
		{"internal open medicos GET", internal, "GET", "/api/medicos/doctors", "", s.medicos, "", ""},
		{"internal authed turnos", internal, "GET", "/api/turnos/appointments", validToken(t, "p-7"), s.turnos, "p-7", "patient"},
		{"internal authed historia", internal, "GET", "/api/historia/records", validToken(t, "p-7"), s.historia, "p-7", "patient"},
		{"internal partner v1", internal, "POST", "/v1/appointments", "", s.turnos, "", ""},
		{"internal notifications v1", internal, "GET", "/v1/templates", "", s.notif, "", ""},
		{"public partner v1", public, "POST", "/v1/appointments", "", s.turnos, "", ""},
		{"public partner v1 with token", public, "GET", "/v1/usage", validToken(t, "p-7"), s.turnos, "", ""},
		{"public notifications v1", public, "GET", "/v1/templates", "", s.notif, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := c.up.calls.Load()
			rec := do(c.h, c.method, c.path, c.token, spoof...)
			if rec.Code != 200 || c.up.calls.Load() != before+1 {
				t.Fatalf("status %d, upstream not reached", rec.Code)
			}
			if p, r := c.up.seen(&c.up.patient), c.up.seen(&c.up.role); p != c.wantP || r != c.wantR {
				t.Fatalf("upstream saw X-Patient-Id=%q X-User-Role=%q, want %q %q", p, r, c.wantP, c.wantR)
			}
		})
	}
}

func TestPortalRoutesApplyTheirAuthPolicy(t *testing.T) {
	s := newStack(t)
	h := s.cfg.internalHandler()
	tok := validToken(t, "p-1")
	cases := []struct {
		method, path, token string
		up                  *recorder
		wantStatus          int
		wantPath            string
	}{
		{"POST", "/api/pacientes/auth/register", "", s.pacientes, 200, "/auth/register"},
		{"POST", "/api/pacientes/auth/login", "", s.pacientes, 200, "/auth/login"},
		{"GET", "/api/pacientes/auth/login", "", s.pacientes, 401, ""},
		{"POST", "/api/pacientes/auth%2flogin", "", s.pacientes, 401, ""},
		{"GET", "/api/pacientes/me", "", s.pacientes, 401, ""},
		{"GET", "/api/pacientes/me", tok, s.pacientes, 200, "/me"},
		{"GET", "/api/medicos/doctors", "", s.medicos, 200, "/doctors"},
		{"POST", "/api/medicos/doctors", "", s.medicos, 401, ""},
		{"POST", "/api/medicos/doctors", tok, s.medicos, 200, "/doctors"},
		{"GET", "/api/turnos/appointments", "", s.turnos, 401, ""},
		{"POST", "/api/turnos/appointments", "", s.turnos, 401, ""},
		{"POST", "/api/turnos/appointments", tok, s.turnos, 200, "/appointments"},
		{"GET", "/api/historia/records/1", "", s.historia, 401, ""},
		{"GET", "/api/historia/records/1", tok, s.historia, 200, "/records/1"},
	}
	for _, c := range cases {
		before := c.up.calls.Load()
		rec := do(h, c.method, c.path, c.token)
		if rec.Code != c.wantStatus {
			t.Fatalf("%s %s token=%v: status %d %s", c.method, c.path, c.token != "", rec.Code, rec.Body)
		}
		reached := c.up.calls.Load() == before+1
		if c.wantStatus == 200 && (!reached || rec.Header().Get("X-Seen-Path") != c.wantPath) {
			t.Fatalf("%s %s: reached=%v path=%q", c.method, c.path, reached, rec.Header().Get("X-Seen-Path"))
		}
		if c.wantStatus == 401 && (reached || errorCode(t, rec) != "UNAUTHORIZED") {
			t.Fatalf("%s %s: rejected request reached upstream or wrong body", c.method, c.path)
		}
	}
}

// An expired token left in the browser must not block login or the catalog.
func TestOpenRoutesIgnoreInvalidTokens(t *testing.T) {
	s := newStack(t)
	h := s.cfg.internalHandler()
	expired := validClaims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	tok := signHS(t, jwt.SigningMethodHS256, expired, testSecret)
	for _, c := range [][2]string{{"POST", "/api/pacientes/auth/login"}, {"GET", "/api/medicos/doctors"}} {
		if rec := do(h, c[0], c[1], tok, "X-Patient-Id", "p-evil"); rec.Code != 200 {
			t.Fatalf("%s %s: %d", c[0], c[1], rec.Code)
		}
	}
	if s.medicos.seen(&s.medicos.patient) != "" {
		t.Fatal("anonymous request forwarded an identity")
	}
}

func TestHealthzReportsPortalAuthOnBothListeners(t *testing.T) {
	s := newStack(t)
	for name, h := range map[string]http.Handler{"internal": s.cfg.internalHandler(), "public": s.cfg.publicHandler()} {
		rec := do(h, "GET", "/healthz", "")
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if body["portalAuthImplemented"] != true || body["status"] != "ok" || body["service"] != "gateway" {
			t.Fatalf("%s: %v", name, body)
		}
	}
}

func TestMetricsOnlyOnInternalListener(t *testing.T) {
	s := newStack(t)
	rec := do(s.cfg.internalHandler(), "GET", "/metrics", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Fatalf("internal /metrics: %d", rec.Code)
	}
	if rec := do(s.cfg.publicHandler(), "GET", "/metrics", ""); rec.Code != 404 {
		t.Fatalf("public /metrics: %d", rec.Code)
	}
}

func metricsText(t *testing.T, cfg config) string {
	t.Helper()
	rec := do(cfg.internalHandler(), "GET", "/metrics", "")
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}
