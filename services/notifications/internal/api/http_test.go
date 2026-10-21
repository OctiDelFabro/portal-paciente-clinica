package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Estos casos se resuelven antes de llegar a la base, por eso no la necesitan.
func newHandler(rate int) http.Handler {
	mux := http.NewServeMux()
	(&HTTP{Keys: map[string]string{"team-a": "demo-a"}, Rate: rate, Health: func() error { return nil }}).Register(mux)
	return mux
}

func do(h http.Handler, method, path, key, idem, ctype, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const validBody = `{"templateId":"operation-confirmed.v1","recipient":{"email":"a@example.test"},"parameters":{"operationName":"Turno","reference":"T-1"}}`

func TestPublicAPIRejectionsFollowContract(t *testing.T) {
	h := newHandler(60)
	cases := []struct {
		name             string
		key, idem, ctype string
		body             string
		wantStatus       int
		wantCode         string
	}{
		{"no key", "", "k1", "application/json", validBody, 401, "UNAUTHORIZED"},
		{"bad key", "nope", "k1", "application/json", validBody, 401, "UNAUTHORIZED"},
		{"bad idempotency key", "demo-a", "has space", "application/json", validBody, 400, "INVALID_IDEMPOTENCY_KEY"},
		{"wrong media", "demo-a", "k1", "text/plain", validBody, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"unknown field", "demo-a", "k1", "application/json", `{"templateId":"operation-confirmed.v1","subject":"x"}`, 400, "INVALID_JSON"},
		{"free text template", "demo-a", "k1", "application/json", strings.Replace(validBody, "operation-confirmed.v1", "custom.v1", 1), 422, "VALIDATION_ERROR"},
		{"too large", "demo-a", "k1", "application/json", `{"templateId":"` + strings.Repeat("x", 17000) + `"}`, 413, "PAYLOAD_TOO_LARGE"},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/v1/notifications", c.key, c.idem, c.ctype, c.body)
		if rec.Code != c.wantStatus || !strings.Contains(rec.Body.String(), c.wantCode) {
			t.Errorf("%s: got %d %s", c.name, rec.Code, rec.Body)
		}
	}
}

func TestRateLimitReturnsRetryAfter(t *testing.T) {
	h := newHandler(2)
	for i := 0; i < 2; i++ {
		if rec := do(h, "GET", "/v1/templates", "demo-a", "", "", ""); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := do(h, "GET", "/v1/templates", "demo-a", "", "", "")
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("want 429 with Retry-After, got %d %v", rec.Code, rec.Header())
	}
}

func TestHealthIsPublicAndReportsServiceMode(t *testing.T) {
	rec := do(newHandler(60), "GET", "/healthz", "", "", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mode":"service"`) || !strings.Contains(rec.Body.String(), `"simulated":false`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
