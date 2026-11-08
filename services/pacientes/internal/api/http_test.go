package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"portal-paciente-clinica/services/pacientes/internal/repository"
	"portal-paciente-clinica/services/pacientes/internal/service"
)

type memStore struct {
	mu   sync.Mutex
	byID map[string]repository.Patient
	down bool
}

func (m *memStore) Create(_ context.Context, p repository.Patient) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return errors.New("db down")
	}
	for _, o := range m.byID {
		if p.Email != "" && o.Email == p.Email {
			return repository.ErrEmailTaken
		}
	}
	m.byID[p.ID] = p
	return nil
}

func (m *memStore) ByEmail(_ context.Context, email string) (repository.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.byID {
		if p.Email == email {
			return p, nil
		}
	}
	return repository.Patient{}, repository.ErrNotFound
}

func (m *memStore) ByID(_ context.Context, id string) (repository.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.byID[id]; ok {
		return p, nil
	}
	return repository.Patient{}, repository.ErrNotFound
}

func (m *memStore) SetPasswordHash(_ context.Context, id, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.byID[id]
	p.PasswordHash = hash
	m.byID[id] = p
	return nil
}

func (m *memStore) SetRole(_ context.Context, id, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.byID[id]
	p.Role = role
	m.byID[id] = p
	return nil
}

func newMux(t *testing.T) (*http.ServeMux, *memStore) {
	t.Helper()
	store := &memStore{byID: map[string]repository.Patient{}}
	issuer, err := service.NewIssuer("0123456789abcdef0123456789abcdef", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	svc := &service.Service{Store: store, Tokens: issuer, Now: time.Now, NewID: func() string { return "x1" }, Cost: bcrypt.MinCost,
		Limiter: &service.Limiter{Max: 5, Window: 15 * time.Minute}}
	if err := svc.SeedDemo(context.Background(), "demo-pass-1"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&HTTP{Service: svc, Health: func() error { return nil }}).Register(mux)
	return mux, store
}

func call(mux http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid error body %q", rec.Body.String())
	}
	return body.Error.Code
}

const registerBody = `{"email":"new@example.test","password":"s3cret-pass","fullName":"New Patient","documentNumber":"40111222","birthDate":"1995-02-03"}`

func TestRegisterReturns201WithPatientID(t *testing.T) {
	mux, store := newMux(t)
	rec := call(mux, "POST", "/auth/register", registerBody, nil)
	if rec.Code != 201 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["patientId"] != "pat-x1" || len(body) != 1 {
		t.Fatalf("unexpected body %v", body)
	}
	if strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(rec.Body.String(), store.byID["pat-x1"].PasswordHash) {
		t.Fatal("response must not expose password or hash")
	}
}

func TestRegisterErrors(t *testing.T) {
	mux, _ := newMux(t)
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"duplicate email", strings.Replace(registerBody, "new@example.test", "paciente1@example.test", 1), 409, "EMAIL_TAKEN"},
		{"short password", strings.Replace(registerBody, "s3cret-pass", "short", 1), 422, "VALIDATION_ERROR"},
		{"unknown field", `{"email":"a@example.test","role":"admin"}`, 400, "INVALID_JSON"},
		{"not json", `nope`, 400, "INVALID_JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(mux, "POST", "/auth/register", tc.body, nil)
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s", rec.Code, rec.Body)
			}
		})
	}
	req := httptest.NewRequest("POST", "/auth/register", strings.NewReader(registerBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Fatalf("missing content type: got %d", rec.Code)
	}
}

func TestRegisterStorageFailureIs503(t *testing.T) {
	mux, store := newMux(t)
	store.down = true
	rec := call(mux, "POST", "/auth/register", registerBody, nil)
	if rec.Code != 503 || errorCode(t, rec) != "SERVICE_UNAVAILABLE" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestLoginReturnsBearerToken(t *testing.T) {
	mux, _ := newMux(t)
	rec := call(mux, "POST", "/auth/login", `{"email":"paciente1@example.test","password":"demo-pass-1"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var body struct {
		AccessToken string `json:"accessToken"`
		TokenType   string `json:"tokenType"`
		ExpiresIn   int    `json:"expiresIn"`
		Patient     struct {
			PatientID, FullName, Email string
		} `json:"patient"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.AccessToken == "" || body.TokenType != "Bearer" || body.ExpiresIn != 3600 ||
		body.Patient.PatientID != "pat-001" || body.Patient.Email != "paciente1@example.test" || body.Patient.FullName == "" {
		t.Fatalf("unexpected body %s", rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token responses must not be cached")
	}
}

func TestLoginInvalidCredentialsIdenticalForUnknownAndWrong(t *testing.T) {
	mux, _ := newMux(t)
	unknown := call(mux, "POST", "/auth/login", `{"email":"nobody@example.test","password":"demo-pass-1"}`, nil)
	wrong := call(mux, "POST", "/auth/login", `{"email":"paciente1@example.test","password":"bad-pass-1"}`, nil)
	if unknown.Code != 401 || wrong.Code != 401 || unknown.Body.String() != wrong.Body.String() || errorCode(t, wrong) != "INVALID_CREDENTIALS" {
		t.Fatalf("got %d %s / %d %s", unknown.Code, unknown.Body, wrong.Code, wrong.Body)
	}
}

func TestLoginRateLimitedAfterFiveFailures(t *testing.T) {
	mux, _ := newMux(t)
	for i := 0; i < 5; i++ {
		call(mux, "POST", "/auth/login", `{"email":"paciente2@example.test","password":"bad-pass-1"}`, nil)
	}
	rec := call(mux, "POST", "/auth/login", `{"email":"paciente2@example.test","password":"demo-pass-1"}`, nil)
	if rec.Code != 429 || errorCode(t, rec) != "RATE_LIMITED" || rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("got %d %s retry=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	other := call(mux, "POST", "/auth/login", `{"email":"paciente1@example.test","password":"demo-pass-1"}`, nil)
	if other.Code != 200 {
		t.Fatalf("limiter must be per email, got %d", other.Code)
	}
}

func TestMeRequiresPatientHeader(t *testing.T) {
	mux, _ := newMux(t)
	if rec := call(mux, "GET", "/me", "", nil); rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	rec := call(mux, "GET", "/me", "", map[string]string{"X-Patient-Id": "pat-002", "X-User-Role": "patient"})
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body["patientId"] != "pat-002" || body["email"] != "paciente2@example.test" || body["enabled"] != false {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("$2a$")) || bytes.Contains(rec.Body.Bytes(), []byte("password")) {
		t.Fatal("profile must not expose password hash")
	}
	if rec := call(mux, "GET", "/me", "", map[string]string{"X-Patient-Id": "pat-404"}); rec.Code != 404 {
		t.Fatalf("unknown patient: got %d", rec.Code)
	}
}

func TestContactForInternalUse(t *testing.T) {
	mux, _ := newMux(t)
	rec := call(mux, "GET", "/patients/pat-001/contact", "", nil)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body["patientId"] != "pat-001" || body["email"] != "paciente1@example.test" || body["fullName"] == "" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	minor := call(mux, "GET", "/patients/pat-003/contact", "", nil)
	_ = json.Unmarshal(minor.Body.Bytes(), &body)
	if minor.Code != 200 || body["email"] != nil || body["responsiblePatientId"] != "pat-001" {
		t.Fatalf("minor contact: %s", minor.Body)
	}
	if rec := call(mux, "GET", "/patients/pat-404/contact", "", nil); rec.Code != 404 {
		t.Fatalf("unknown: got %d", rec.Code)
	}
	// Through the gateway every request carries X-Patient-Id: a patient cannot read another one's contact.
	if rec := call(mux, "GET", "/patients/pat-002/contact", "", map[string]string{"X-Patient-Id": "pat-001"}); rec.Code != 403 {
		t.Fatalf("cross-patient read: got %d", rec.Code)
	}
	// A doctor CAN read patient contact for agenda lookup.
	if rec := call(mux, "GET", "/patients/pat-002/contact", "", map[string]string{"X-Patient-Id": "doc-001", "X-User-Role": "doctor"}); rec.Code != 200 {
		t.Fatalf("doctor reading patient contact: got %d", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	mux, _ := newMux(t)
	if rec := call(mux, "GET", "/healthz", "", nil); rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
}
