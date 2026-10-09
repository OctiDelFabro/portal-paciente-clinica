package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const validBody = `{"templateId":"operation-confirmed.v1","recipient":{"email":"paciente@example.com"},"parameters":{"operationName":"Reserva","reference":"REF-1"}}`

func setup() (*App, *time.Time) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return NewApp(Config{Keys: map[string]string{"a": "key-a", "b": "key-b"}, Result: "sent", Delay: time.Second, Rate: 60, Now: func() time.Time { return now }}), &now
}
func call(a *App, method, path, body, key, idem string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", key)
	r.Header.Set("Idempotency-Key", idem)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func notification(t *testing.T, w *httptest.ResponseRecorder) Notification {
	t.Helper()
	var n Notification
	if err := json.Unmarshal(w.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestAcceptedReplayAndResult(t *testing.T) {
	a, now := setup()
	w := call(a, "POST", "/v1/notifications", validBody, "key-a", "r1")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	n := notification(t, w)
	if n.Status != "accepted" || !n.Simulated || w.Header().Get("Location") == "" {
		t.Fatal(n)
	}
	reordered := `{"parameters":{"reference":"REF-1","operationName":"Reserva"},"recipient":{"email":"paciente@example.com"},"templateId":"operation-confirmed.v1"}`
	w = call(a, "POST", "/v1/notifications", reordered, "key-a", "r1")
	if w.Code != 202 || notification(t, w).ID != n.ID || w.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal(w)
	}
	*now = now.Add(2 * time.Second)
	w = call(a, "GET", "/v1/notifications/"+n.ID, "", "key-a", "")
	if notification(t, w).Status != "sent" {
		t.Fatal(w.Body.String())
	}
	w = call(a, "POST", "/v1/notifications", validBody, "key-a", "r1")
	if notification(t, w).Status != "accepted" {
		t.Fatal("replay debe conservar la respuesta original")
	}
}
func TestConcurrentDeduplication(t *testing.T) {
	a, _ := setup()
	const count = 30
	ids := make(chan string, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call(a, "POST", "/v1/notifications", validBody, "key-a", "same")
			if w.Code != 202 {
				ids <- "ERROR"
				return
			}
			var n Notification
			_ = json.Unmarshal(w.Body.Bytes(), &n)
			ids <- n.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if id == "ERROR" {
			t.Fatal(id)
		}
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicacion concurrente")
		}
	}
	if len(a.notifications) != 1 {
		t.Fatal("se creo mas de una solicitud")
	}
}
func TestValidationAndConflict(t *testing.T) {
	a, _ := setup()
	cases := []struct {
		body, idem string
		code       int
	}{
		{validBody, "", 400},
		{"{", "a", 400},
		{strings.Replace(validBody, "REF-1", strings.Repeat("x", 81), 1), "b", 422},
		{strings.Replace(validBody, "paciente@example.com", "sin-correo", 1), "c", 422},
		{strings.Replace(validBody, "operation-confirmed.v1", "unknown", 1), "d", 422},
		{strings.Replace(validBody, "\"reference\":\"REF-1\"", "\"unexpected\":\"x\"", 1), "e", 422},
		{strings.Replace(validBody, "\"templateId\"", "\"unknown\"", 1), "f", 400},
		{strings.Repeat(" ", 16385), "g", 413},
		{validBody + " {}", "h", 400},
	}
	for _, c := range cases {
		w := call(a, "POST", "/v1/notifications", c.body, "key-a", c.idem)
		if w.Code != c.code {
			t.Fatalf("%d != %d: %s", w.Code, c.code, w.Body.String())
		}
	}
	call(a, "POST", "/v1/notifications", validBody, "key-a", "original")
	w := call(a, "POST", "/v1/notifications", strings.Replace(validBody, "REF-1", "REF-2", 1), "key-a", "original")
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestIsolationExpirationAndFailure(t *testing.T) {
	a, now := setup()
	n := notification(t, call(a, "POST", "/v1/notifications", validBody, "key-a", "r1"))
	if call(a, "GET", "/v1/notifications/"+n.ID, "", "key-b", "").Code != 404 {
		t.Fatal("acceso entre consumidores")
	}
	other := notification(t, call(a, "POST", "/v1/notifications", validBody, "key-b", "r1"))
	if n.ID == other.ID {
		t.Fatal("las claves deben tener alcance por consumidor")
	}
	*now = now.Add(24 * time.Hour)
	renewed := notification(t, call(a, "POST", "/v1/notifications", validBody, "key-a", "r1"))
	if renewed.ID == n.ID {
		t.Fatal("ventana no vencio")
	}
	a.cfg.Result = "failed"
	*now = now.Add(time.Minute)
	if notification(t, call(a, "GET", "/v1/notifications/"+renewed.ID, "", "key-a", "")).Status != "failed" {
		t.Fatal("fallo simulado")
	}
}
func TestAuthRateAndContentType(t *testing.T) {
	a, _ := setup()
	if call(a, "GET", "/v1/templates", "", "invalid", "").Code != 401 {
		t.Fatal("auth")
	}
	for i := 0; i < 60; i++ {
		if call(a, "GET", "/v1/templates", "", "key-a", "").Code != 200 {
			t.Fatal("limite prematuro", i)
		}
	}
	w := call(a, "GET", "/v1/templates", "", "key-a", "")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit")
	}
	r := httptest.NewRequest("POST", "/v1/notifications", strings.NewReader(validBody))
	r.Header.Set("X-API-Key", "key-b")
	r.Header.Set("Idempotency-Key", "key")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	if call(a, http.MethodGet, "/healthz", "", "", "").Code != 200 {
		t.Fatal("health")
	}
}
func TestTemplateCatalog(t *testing.T) {
	a, _ := setup()
	w := call(a, "GET", "/v1/templates", "", "key-a", "")
	var c struct {
		Templates []Template `json:"templates"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if len(c.Templates) != 3 {
		t.Fatal(fmt.Sprint(c))
	}
	for _, tmpl := range catalog {
		params := map[string]string{"operationName": "Operacion", "reference": "REF"}
		if tmpl.ID == "operation-reminder.v1" {
			params["scheduledAt"] = "2026-10-10T12:00:00-03:00"
		}
		b, _ := json.Marshal(Request{TemplateID: tmpl.ID, Recipient: Recipient{Email: "usuario@example.com"}, Parameters: params})
		if call(a, "POST", "/v1/notifications", string(b), "key-a", tmpl.ID).Code != 202 {
			t.Fatal(tmpl.ID)
		}
	}
}
