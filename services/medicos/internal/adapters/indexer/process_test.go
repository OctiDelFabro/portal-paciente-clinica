package indexer

import (
	"context"
	"errors"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/medicos/internal/app"
)

type fakeHandler struct {
	indexed  []string
	bookings []app.Booking
	err      error
	dup      bool
}

func (f *fakeHandler) IndexDoctor(_ context.Context, id string) error {
	f.indexed = append(f.indexed, id)
	return f.err
}
func (f *fakeHandler) HandleBooking(_ context.Context, b app.Booking) (bool, error) {
	f.bookings = append(f.bookings, b)
	return f.dup, f.err
}

const (
	profileEvent = `{"eventId":"e-1","eventType":"medicos.perfil-actualizado.v1","version":1,"occurredAt":"2026-12-01T10:00:00Z","correlationId":"c","data":{"doctorId":"doc-001","fullName":"X","specialties":["a"],"acceptsMinors":false,"active":true,"profileVersion":2}}`
	bookedEvent  = `{"eventId":"e-2","eventType":"turnos.reservado.v1","version":1,"occurredAt":"2026-12-01T10:00:00Z","correlationId":"c","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-03T10:00:00Z","endAt":"2026-12-03T10:30:00Z","appointmentVersion":1}}`
	cancelEvent  = `{"eventId":"e-3","eventType":"turnos.cancelado.v1","version":1,"occurredAt":"2026-12-01T11:00:00Z","correlationId":"c","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-03T10:00:00Z","endAt":"2026-12-03T10:30:00Z","appointmentVersion":2,"reasonCode":"patient"}}`
)

func TestProcessRoutesEachEventType(t *testing.T) {
	h := &fakeHandler{}
	if res, at, err := Process(context.Background(), h, app.RoutingProfileUpdated, []byte(profileEvent)); err != nil || res != "ok" || !at.Equal(time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("profile: res=%s at=%s err=%v", res, at, err)
	}
	if len(h.indexed) != 1 || h.indexed[0] != "doc-001" {
		t.Fatalf("profile event must reindex doc-001, got %v", h.indexed)
	}
	if _, _, err := Process(context.Background(), h, messaging.RoutingBooked, []byte(bookedEvent)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Process(context.Background(), h, messaging.RoutingCancel, []byte(cancelEvent)); err != nil {
		t.Fatal(err)
	}
	want := []app.Booking{
		{TurnID: "t-1", DoctorID: "doc-001", Start: time.Date(2026, 12, 3, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 12, 3, 10, 30, 0, 0, time.UTC), Active: true, Version: 1},
		{TurnID: "t-1", DoctorID: "doc-001", Start: time.Date(2026, 12, 3, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 12, 3, 10, 30, 0, 0, time.UTC), Active: false, Version: 2},
	}
	if len(h.bookings) != 2 || h.bookings[0] != want[0] || h.bookings[1] != want[1] {
		t.Fatalf("bookings %+v", h.bookings)
	}
	h.dup = true
	if res, _, err := Process(context.Background(), h, messaging.RoutingBooked, []byte(bookedEvent)); err != nil || res != "duplicate" {
		t.Fatalf("redelivery: res=%s err=%v", res, err)
	}
}

func TestProcessClassifiesFailures(t *testing.T) {
	tests := []struct {
		name, key, body string
		handlerErr      error
		unprocessable   bool
	}{
		{"malformed json", messaging.RoutingBooked, `{"broken":`, nil, true},
		{"missing event id", app.RoutingProfileUpdated, `{"data":{"doctorId":"doc-001"}}`, nil, true},
		{"booking without dates", messaging.RoutingBooked, `{"eventId":"e","occurredAt":"2026-12-01T10:00:00Z","data":{"turnId":"t","doctorId":"doc-001","appointmentVersion":1}}`, nil, true},
		{"unknown routing key", "otro.evento.v1", profileEvent, nil, true},
		{"unknown doctor", app.RoutingProfileUpdated, profileEvent, app.ErrUnknownDoctor, true},
		{"search engine down", app.RoutingProfileUpdated, profileEvent, app.ErrSearchUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Process(context.Background(), &fakeHandler{err: tt.handlerErr}, tt.key, []byte(tt.body))
			if err == nil || errors.Is(err, ErrUnprocessable) != tt.unprocessable {
				t.Fatalf("want unprocessable=%v, got %v", tt.unprocessable, err)
			}
		})
	}
}
