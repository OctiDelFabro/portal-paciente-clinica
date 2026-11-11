package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type portalDirectory struct {
	seedDirectory
	people        map[string]domain.Patient
	acceptsMinors bool
}

func (d portalDirectory) Patient(_ context.Context, id string) (domain.Patient, error) {
	p, ok := d.people[id]
	if !ok {
		return p, ErrUnknownPatient
	}
	return p, nil
}
func (d portalDirectory) Doctor(ctx context.Context, id string) (domain.Doctor, error) {
	v, err := d.seedDirectory.Doctor(ctx, id)
	v.AcceptsMinors = d.acceptsMinors
	return v, err
}
func portalService() (*Service, *memStore, portalDirectory) {
	s, store := newService()
	d := portalDirectory{people: map[string]domain.Patient{}}
	for _, id := range []string{"parent", "other", "child", "reception", "doctor"} {
		d.people[id] = domain.Patient{ID: id, Role: "patient", Enabled: true, BirthDate: time.Date(1990, 1, 1, 0, 0, 0, 0, domain.ClinicZone)}
	}
	p := d.people["child"]
	p.BirthDate = time.Date(2015, 1, 1, 0, 0, 0, 0, domain.ClinicZone)
	p.ResponsibleID = "parent"
	d.people[p.ID] = p
	for _, id := range []string{"reception", "doctor"} {
		p = d.people[id]
		p.Role = id
		d.people[id] = p
	}
	s.Directory = d
	return s, store, d
}
func TestPortalScopeRechecksVerifiedGuardianAndStaff(t *testing.T) {
	s, _, d := portalService()
	ctx := context.Background()
	start := fixedNow.Add(48 * time.Hour)
	// Adult-only doctor rejects an actual minor even when reception assigns.
	if _, err := s.PortalBook(ctx, "reception", "reception", "child", "d-1", start); !errors.Is(err, domain.ErrMinorsNotAccepted) {
		t.Fatalf("minor accepted: %v", err)
	}
	d.acceptsMinors = true
	s.Directory = d
	a, err := s.PortalBook(ctx, "patient", "parent", "child", "d-1", start)
	if err != nil {
		t.Fatal(err)
	}
	// A linked child appointment can be read/cancelled by the verified adult.
	if _, err := s.PortalGet(ctx, "patient", "parent", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PortalGet(ctx, "patient", "other", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrelated read: %v", err)
	}
	p := d.people["parent"]
	p.Enabled = false
	d.people[p.ID] = p
	if _, err := s.PortalCancel(ctx, "patient", "parent", a.ID, ""); err == nil {
		t.Fatal("revoked adult cancelled dependent")
	}
	p.Enabled = true
	d.people[p.ID] = p
	if _, err := s.PortalBook(ctx, "doctor", "doctor", "parent", "d-1", start); !errors.Is(err, ErrForbidden) {
		t.Fatalf("doctor assigned: %v", err)
	}
	if _, err := s.PortalGet(ctx, "reception", "reception", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PortalCancel(ctx, "patient", "parent", a.ID, ""); err != nil {
		t.Fatal(err)
	}
}
func TestRescheduleRollsBackOriginalAndOutboxOnConflict(t *testing.T) {
	s, store, _ := portalService()
	ctx := context.Background()
	start := fixedNow.Add(48 * time.Hour)
	a, err := s.PortalBook(ctx, "patient", "parent", "parent", "d-1", start)
	if err != nil {
		t.Fatal(err)
	}
	occupied, err := s.PortalBook(ctx, "patient", "other", "other", "d-1", start.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []time.Time{occupied.StartAt, fixedNow.Add(time.Hour)} {
		before := len(store.outbox)
		if _, err := s.Reschedule(ctx, "patient", "parent", a.ID, target); err == nil {
			t.Fatal("invalid move accepted")
		}
		got, _ := store.Get(ctx, a.ID)
		if got.Status != domain.StatusBooked || len(store.outbox) != before {
			t.Fatal("failed reschedule changed original/outbox")
		}
	}
	moved, err := s.Reschedule(ctx, "reception", "reception", a.ID, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	old, _ := store.Get(ctx, a.ID)
	if moved.ID == a.ID || old.Status != domain.StatusCancelled || moved.Status != domain.StatusBooked {
		t.Fatal("replacement lifecycle wrong")
	}
	events := store.outbox[len(store.outbox)-2:]
	if events[0].RoutingKey != messaging.RoutingCancel || events[1].RoutingKey != messaging.RoutingRescheduled {
		t.Fatalf("wrong events: %+v", events)
	}
	var event struct {
		Data struct {
			Operation string `json:"operation"`
		}
	}
	if err := json.Unmarshal(events[0].Payload, &event); err != nil || event.Data.Operation != "reschedule" {
		t.Fatal("missing internal reschedule marker", err)
	}
	var movedEvent eventEnvelope
	if err := json.Unmarshal(events[1].Payload, &movedEvent); err != nil || movedEvent.Data.PreviousTurnID != a.ID || movedEvent.Data.PreviousStartAt != a.StartAt.Format(time.RFC3339) || movedEvent.Data.PreviousEndAt != a.EndAt.Format(time.RFC3339) {
		t.Fatal("reschedule event does not match contract", err)
	}
	if _, err := s.Reschedule(ctx, "patient", "parent", a.ID, start.Add(90*time.Minute)); err == nil {
		t.Fatal("cancelled appointment rescheduled twice")
	}
}

func TestQuoteRejectsUnbookableTime(t *testing.T) {
	s, _, _ := portalService()
	ctx := context.Background()
	if _, err := s.Quote(ctx, "d-1", fixedNow.Add(48*time.Hour+12*time.Hour)); !errors.Is(err, domain.ErrOutsideSchedule) {
		t.Fatalf("quote outside hours: %v", err)
	}
}
