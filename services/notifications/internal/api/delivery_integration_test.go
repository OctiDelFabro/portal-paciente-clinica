//go:build integration

package api

import (
	"context"
	"encoding/json"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"net/http"
	"portal-paciente-clinica/services/notifications/internal/mailer"
	"strings"
	"testing"
	"time"
)

// Positive external contracts use isolated consumer keys and an isolated SMTP
// receiver. No production API key or real mailbox is involved.
func TestTwoConsumersIdempotencyIsolationAndActualSMTPDelivery(t *testing.T) {
	ctx := context.Background()
	repo := startRepo(t)
	svc := newService(repo)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{Image: "axllent/mailpit:v1.27", ExposedPorts: []string{"1025/tcp", "8025/tcp"}, WaitingFor: wait.ForHTTP("/api/v1/info").WithPort("8025/tcp")}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	smtpAddr, err := c.PortEndpoint(ctx, "1025/tcp", "")
	if err != nil {
		t.Fatal(err)
	}
	httpAddr, err := c.PortEndpoint(ctx, "8025/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	svc.Mailer = mailer.SMTP{Addr: smtpAddr, From: "portal@example.test"}
	mux := http.NewServeMux()
	(&HTTP{Service: svc, Keys: map[string]string{"team-a": "demo-a", "team-b": "demo-b"}, Rate: 1000, Health: func() error { return nil }}).Register(mux)
	a := do(mux, "POST", "/v1/notifications", "demo-a", "shared-key", "application/json", validBody)
	if a.Code != 202 {
		t.Fatalf("accept A: %d %s", a.Code, a.Body)
	}
	location := a.Header().Get("Location")
	if replay := do(mux, "POST", "/v1/notifications", "demo-a", "shared-key", "application/json", validBody); replay.Code != 202 || replay.Header().Get("Location") != location {
		t.Fatal("replay changed resource")
	}
	if w := do(mux, "GET", location, "demo-b", "", "", ""); w.Code != 404 {
		t.Fatal("consumer B read A's job", w.Code)
	}
	b := do(mux, "POST", "/v1/notifications", "demo-b", "shared-key", "application/json", strings.Replace(validBody, "a@example.test", "b@example.test", 1))
	if b.Code != 202 || b.Header().Get("Location") == location {
		t.Fatalf("consumer key scope: %d %s", b.Code, b.Body)
	}
	if w := do(mux, "POST", "/v1/notifications", "demo-a", "shared-key", "application/json", strings.Replace(validBody, "T-1", "T-2", 1)); w.Code != 409 {
		t.Fatal("changed replay accepted", w.Code)
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() { defer close(done); svc.RunDelivery(worker, 10*time.Millisecond) }()
	waitFor(t, func() bool {
		w := do(mux, "GET", location, "demo-a", "", "", "")
		return w.Code == 200 && strings.Contains(w.Body.String(), `"status":"sent"`)
	})
	client := &http.Client{Timeout: time.Second}
	waitFor(t, func() bool {
		r, e := client.Get(httpAddr + "/api/v1/messages")
		if e != nil {
			return false
		}
		defer r.Body.Close()
		var mailbox struct {
			Total int `json:"total"`
		}
		return json.NewDecoder(r.Body).Decode(&mailbox) == nil && mailbox.Total == 2
	})
	var rows int
	_ = repo.DB.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&rows)
	if rows != 2 {
		t.Fatalf("idempotent requests created %d jobs", rows)
	}
}
