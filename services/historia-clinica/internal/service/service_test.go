package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

const bookedBody = `{"eventId":"0b8f7c3a-1111-4222-8333-444455556666","eventType":"turnos.reservado.v1","version":1,"occurredAt":"2026-10-20T12:00:00Z","correlationId":"c-1","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-10-25T10:00:00Z","endAt":"2026-10-25T10:30:00Z","appointmentVersion":1}}`

func TestParseEventRejectsUnprocessableMessages(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"broken json", `{"broken":`},
		{"unknown event type", strings.Replace(bookedBody, "turnos.reservado.v1", "turnos.otro.v1", 1)},
		{"unsupported version", strings.Replace(bookedBody, `"version":1`, `"version":2`, 1)},
		{"missing event id", strings.Replace(bookedBody, "0b8f7c3a-1111-4222-8333-444455556666", "", 1)},
		{"missing turn id", strings.Replace(bookedBody, `"turnId":"t-1"`, `"turnId":""`, 1)},
		{"missing patient id", strings.Replace(bookedBody, `"patientId":"pat-001"`, `"patientId":""`, 1)},
		{"missing doctor id", strings.Replace(bookedBody, `"doctorId":"doc-001"`, `"doctorId":""`, 1)},
		{"invalid start", strings.Replace(bookedBody, "2026-10-25T10:00:00Z", "tomorrow", 1)},
		{"cancellation without reason", strings.Replace(bookedBody, "turnos.reservado.v1", "turnos.cancelado.v1", 1)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.ParseEvent([]byte(tt.body)); !errors.Is(err, service.ErrUnprocessable) {
				t.Fatalf("want ErrUnprocessable, got %v", err)
			}
		})
	}
}

func TestParseEventAcceptsBookingAndCancellation(t *testing.T) {
	e, err := service.ParseEvent([]byte(bookedBody))
	if err != nil {
		t.Fatal(err)
	}
	if e.EventID != "0b8f7c3a-1111-4222-8333-444455556666" || e.Data.TurnID != "t-1" || e.Data.PatientID != "pat-001" {
		t.Fatalf("unexpected event %+v", e)
	}
	cancel := strings.Replace(strings.Replace(bookedBody, "turnos.reservado.v1", "turnos.cancelado.v1", 1), `"appointmentVersion":1`, `"appointmentVersion":2,"reasonCode":"patient_request"`, 1)
	if _, err := service.ParseEvent([]byte(cancel)); err != nil {
		t.Fatalf("cancellation rejected: %v", err)
	}
}

// fakeStore records what the service asks the repository to do.
type fakeStore struct {
	changes   []service.Change
	duplicate bool
	err       error
	notes     map[string][]service.Note
	noteErr   error
	timeline  []service.Encounter
	asked     string
}

func (f *fakeStore) Apply(_ context.Context, c service.Change) (bool, error) {
	f.changes = append(f.changes, c)
	return f.duplicate, f.err
}

func (f *fakeStore) AddNote(_ context.Context, appointmentID string, n service.Note) error {
	if f.noteErr != nil {
		return f.noteErr
	}
	if f.notes == nil {
		f.notes = map[string][]service.Note{}
	}
	f.notes[appointmentID] = append(f.notes[appointmentID], n)
	return nil
}

func (f *fakeStore) Encounter(_ context.Context, appointmentID string) (service.Encounter, error) {
	if f.err != nil {
		return service.Encounter{}, f.err
	}
	if f.noteErr != nil {
		return service.Encounter{}, f.noteErr
	}
	return service.Encounter{AppointmentID: appointmentID, DoctorID: "doc-001", Status: service.StatusScheduled}, nil
}

func (f *fakeStore) Timeline(_ context.Context, patientID string) ([]service.Encounter, error) {
	f.asked = patientID
	return f.timeline, nil
}

func (f *fakeStore) Ping(context.Context) error { return nil }

var fixedNow = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func newService(store *fakeStore) *service.Service {
	return &service.Service{Access: service.AccessFunc(func(context.Context, string, string, string) error { return nil }), Store: store, Now: func() time.Time { return fixedNow }, NewID: func() string { return "note-1" }}
}

func TestHandleEventMapsBookingToScheduledEncounter(t *testing.T) {
	store := &fakeStore{}
	e, _ := service.ParseEvent([]byte(bookedBody))
	if _, err := newService(store).HandleEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	want := service.Change{EventID: e.EventID, AppointmentID: "t-1", PatientID: "pat-001", DoctorID: "doc-001",
		StartAt: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC), Status: service.StatusScheduled, At: fixedNow}
	if len(store.changes) != 1 || store.changes[0] != want {
		t.Fatalf("want %+v, got %+v", want, store.changes)
	}
}

func TestHandleEventMapsCancellationToCancelledStatus(t *testing.T) {
	store := &fakeStore{}
	body := strings.Replace(strings.Replace(bookedBody, "turnos.reservado.v1", "turnos.cancelado.v1", 1), `"appointmentVersion":1`, `"appointmentVersion":2,"reasonCode":"patient_request"`, 1)
	e, _ := service.ParseEvent([]byte(body))
	if _, err := newService(store).HandleEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := store.changes[0]; got.Status != service.StatusCancelled || got.CancelReason != "patient_request" {
		t.Fatalf("want cancelled with reason, got %+v", got)
	}
}

func TestHandleEventReportsDuplicatesAndStoreErrors(t *testing.T) {
	e, _ := service.ParseEvent([]byte(bookedBody))
	dup, err := newService(&fakeStore{duplicate: true}).HandleEvent(context.Background(), e)
	if err != nil || !dup {
		t.Fatalf("want duplicate without error, got %v %v", dup, err)
	}
	boom := errors.New("mongo down")
	if _, err := newService(&fakeStore{err: boom}).HandleEvent(context.Background(), e); !errors.Is(err, boom) {
		t.Fatalf("want store error to surface for retry, got %v", err)
	}
}

func TestAddNoteValidatesText(t *testing.T) {
	cases := []struct {
		name string
		text string
		ok   bool
	}{
		{"empty", "   ", false},
		{"too long", strings.Repeat("a", 4001), false},
		{"at limit", strings.Repeat("a", 4000), true},
		{"multibyte at limit", strings.Repeat("ñ", 4000), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			_, err := newService(store).AddNote(context.Background(), "t-1", "doc-001", "doctor", tt.text)
			if tt.ok != (err == nil) {
				t.Fatalf("ok=%v, got %v", tt.ok, err)
			}
			if !tt.ok && !errors.Is(err, service.ErrInvalidNote) {
				t.Fatalf("want ErrInvalidNote, got %v", err)
			}
			if !tt.ok && len(store.notes) != 0 {
				t.Fatal("invalid note was stored")
			}
		})
	}
}

func TestAddNoteStoresNoteWithMetadata(t *testing.T) {
	store := &fakeStore{}
	n, err := newService(store).AddNote(context.Background(), "t-1", "doc-001", "doctor", " Control en 30 dias ")
	if err != nil {
		t.Fatal(err)
	}
	want := service.Note{ID: "note-1", Text: "Control en 30 dias", AuthorRole: "doctor", CreatedAt: fixedNow}
	if n != want || len(store.notes["t-1"]) != 1 || store.notes["t-1"][0] != want {
		t.Fatalf("want %+v stored, got %+v / %+v", want, n, store.notes)
	}
}

func TestAddNoteRejectsUnassignedDoctor(t *testing.T) {
	store := &fakeStore{}
	_, err := newService(store).AddNote(context.Background(), "t-1", "doc-999", "doctor", "Nota de otro medico")
	if !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestTimelineAsksOnlyForTheRequestingPatient(t *testing.T) {
	store := &fakeStore{timeline: []service.Encounter{{AppointmentID: "t-1", PatientID: "pat-001"}}}
	got, err := newService(store).Timeline(context.Background(), "pat-001")
	if err != nil || len(got) != 1 || store.asked != "pat-001" {
		t.Fatalf("got %+v %v asked=%q", got, err, store.asked)
	}
}
