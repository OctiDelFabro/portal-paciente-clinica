//go:build integration

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/internal/platform/patientclient"
	"portal-paciente-clinica/services/notifications/internal/repository"
	"portal-paciente-clinica/services/notifications/internal/service"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableContacts struct{}

func (unavailableContacts) Emails(context.Context, string) ([]string, error) {
	return nil, errors.New("directory offline")
}

type currentReservation struct{ current atomic.Bool }

func (c *currentReservation) Current(context.Context, string, string) (bool, error) {
	return c.current.Load(), nil
}

type countingMailer struct{ sent atomic.Int32 }

func (m *countingMailer) Send(context.Context, string, string, string) error {
	m.sent.Add(1)
	return nil
}

func TestDurableNoticesGuardianScopeAndStaleReminderDelivery(t *testing.T) {
	repo := startRepo(t)
	svc := newService(repo)
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	event := service.Event{EventID: obs.NewID(), EventType: "turnos.reservado.v1", Version: 1}
	event.Data.TurnID = "notice-turn"
	event.Data.PatientID = "child"
	event.Data.DoctorID = "doc"
	event.Data.StartAt = start.Format(time.RFC3339)
	svc.Contacts = unavailableContacts{}
	if _, err := svc.HandleEvent(ctx, event); err == nil {
		t.Fatal("contact outage swallowed")
	}
	list, _, err := repo.Notices(ctx, "child", 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("internal notice delayed by contact failure: %+v %v", list, err)
	}
	svc.Contacts = fixedContacts{}
	if _, err := svc.HandleEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if dup, err := svc.HandleEvent(ctx, event); err != nil || !dup {
		t.Fatal("event replay not idempotent", err)
	}
	var jobs int
	_ = repo.DB.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("duplicated jobs: %d", jobs)
	}
	// Remove the confirmation job so only the deliberately stale reminder is processed below.
	_, _ = repo.DB.Exec(`UPDATE notifications SET status='sent'`)
	current := &currentReservation{}
	current.current.Store(true)
	svc.Appointments = current
	reminder := event
	reminder.EventID = obs.NewID()
	reminder.EventType = "turnos.recordatorio.v1"
	if _, err := svc.HandleEvent(ctx, reminder); err != nil {
		t.Fatal(err)
	}
	current.current.Store(false)
	stale := reminder
	stale.EventID = obs.NewID()
	if _, err := svc.HandleEvent(ctx, stale); err != nil {
		t.Fatal(err)
	}
	notices, _, err := repo.Notices(ctx, "child", 1)
	if err != nil || len(notices) != 2 {
		t.Fatal("stale reminder created notice", err)
	}
	mailer := &countingMailer{}
	svc.Mailer = mailer
	worker, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); svc.RunDelivery(worker, 10*time.Millisecond) }()
	waitFor(t, func() bool {
		var n int
		_ = repo.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE template_id='operation-reminder.v1' AND status='cancelled'`).Scan(&n)
		return n == 1
	})
	cancel()
	<-done
	if mailer.sent.Load() != 0 {
		t.Fatal("stale reminder was emailed")
	}
	// A cancellation whose human-entered reason is 'rescheduled' still generates a notice.
	cancellation := event
	cancellation.EventID = obs.NewID()
	cancellation.EventType = "turnos.cancelado.v1"
	cancellation.Data.ReasonCode = "rescheduled"
	if _, err := svc.HandleEvent(ctx, cancellation); err != nil {
		t.Fatal(err)
	}
	cancellation.EventID = obs.NewID()
	cancellation.Data.Operation = "reschedule"
	if _, err := svc.HandleEvent(ctx, cancellation); err != nil {
		t.Fatal(err)
	}
	notices, _, err = repo.Notices(ctx, "child", 1)
	if err != nil || len(notices) != 3 {
		t.Fatal("reschedule marker suppressed ordinary cancellation or duplicated notice", err)
	}
	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/internal/patients/")
		birth := "1990-01-01"
		responsible := ""
		if id == "child" {
			birth = "2015-01-01"
			responsible = "parent"
		}
		json.NewEncoder(w).Encode(map[string]any{"patientId": id, "enabled": true, "role": "patient", "birthDate": birth, "responsiblePatientId": responsible})
	}))
	defer profileServer.Close()
	svc.Patients = patientclient.New(profileServer.URL, strings.Repeat("k", 32))
	mux := http.NewServeMux()
	(&HTTP{Service: svc}).Register(mux)
	callNotice := func(method, path, caller, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("X-Patient-Id", caller)
		r.Header.Set("X-User-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := callNotice("GET", "/notices?patientId=child", "parent", "patient"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("guardian notices: %d %s", w.Code, w.Body)
	}
	if w := callNotice("GET", "/notices?patientId=child", "other", "patient"); w.Code != 403 {
		t.Fatal("unrelated notice read", w.Code)
	}
	if w := callNotice("GET", "/notices?patientId=child", "reception", "reception"); w.Code != 403 {
		t.Fatal("reception read private notices", w.Code)
	}
	for range 2 {
		if err := repo.ReadNotice(ctx, event.EventID, "child", svc.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.ReadNotice(ctx, event.EventID, "other", svc.Now()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("read marker not scoped", err)
	}
}
