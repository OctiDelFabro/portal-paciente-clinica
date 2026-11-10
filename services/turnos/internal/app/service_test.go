package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// memStore serializa las transacciones con un mutex, igual que el bloqueo de
// recursos de MySQL lo hace para un mismo medico o paciente.
type memStore struct {
	mu       sync.Mutex
	rows     map[string]domain.Appointment
	outbox   []OutboxMessage
	partners map[string]PartnerBooking // por appointment ID
}

type memTx struct{ s *memStore }

func (s *memStore) InTx(ctx context.Context, fn func(Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := map[string]domain.Appointment{}
	for k, v := range s.rows {
		snapshot[k] = v
	}
	partners := map[string]PartnerBooking{}
	for k, v := range s.partners {
		partners[k] = v
	}
	outboxLen := len(s.outbox)
	if err := fn(memTx{s}); err != nil {
		s.rows, s.outbox, s.partners = snapshot, s.outbox[:outboxLen], partners
		return err
	}
	return nil
}
func (s *memStore) Get(ctx context.Context, id string) (domain.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[id]
	if !ok {
		return a, ErrNotFound
	}
	return a, nil
}
func (s *memStore) ActiveForDoctor(_ context.Context, doctorID string, from, to time.Time) ([]domain.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Appointment
	for _, a := range s.rows {
		if a.DoctorID == doctorID && a.Overlaps(from, to) {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s *memStore) ListByPatient(_ context.Context, patientID string) ([]domain.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Appointment{}
	for _, a := range s.rows {
		if a.PatientID == patientID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartAt.After(out[j].StartAt) })
	return out, nil
}
func (s *memStore) ListByDoctor(_ context.Context, doctorID string) ([]domain.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Appointment{}
	for _, a := range s.rows {
		if a.DoctorID == doctorID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartAt.After(out[j].StartAt) })
	return out, nil
}
func (t memTx) LockResources(context.Context, ...string) error { return nil }
func (t memTx) FindActiveOverlapping(_ context.Context, doctorID, patientID string, start, end time.Time) ([]domain.Appointment, error) {
	var out []domain.Appointment
	for _, a := range t.s.rows {
		if (a.DoctorID == doctorID || a.PatientID == patientID) && a.Overlaps(start, end) {
			out = append(out, a)
		}
	}
	return out, nil
}
func (t memTx) GetForUpdate(_ context.Context, id string) (domain.Appointment, error) {
	a, ok := t.s.rows[id]
	if !ok {
		return a, ErrNotFound
	}
	return a, nil
}
func (t memTx) Insert(_ context.Context, a domain.Appointment) error { t.s.rows[a.ID] = a; return nil }
func (t memTx) Update(_ context.Context, a domain.Appointment) error { t.s.rows[a.ID] = a; return nil }
func (t memTx) AppendOutbox(_ context.Context, m OutboxMessage) error {
	t.s.outbox = append(t.s.outbox, m)
	return nil
}

type seedDirectory struct{}

var testDoctor = domain.Doctor{ID: "d-1", Duration: 30 * time.Minute, Schedule: domain.Schedule{
	Weekdays: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
	Open:     9 * time.Hour, Close: 11 * time.Hour}}

func (seedDirectory) Doctor(_ context.Context, id string) (domain.Doctor, error) {
	if id != "d-1" {
		return domain.Doctor{}, ErrUnknownDoctor
	}
	return testDoctor, nil
}
func (seedDirectory) Doctors(context.Context) ([]domain.Doctor, error) {
	return []domain.Doctor{testDoctor}, nil
}

// Patients prefixed "unknown-" are missing from the directory and "disabled-"
// ones are not enabled by reception; every other patient is enabled.
func (seedDirectory) Patient(_ context.Context, id string) (domain.Patient, error) {
	switch {
	case strings.HasPrefix(id, "unknown-"):
		return domain.Patient{}, ErrUnknownPatient
	case strings.HasPrefix(id, "disabled-"):
		return domain.Patient{ID: id, Enabled: false}, nil
	}
	return domain.Patient{ID: id, Enabled: true}, nil
}

var fixedNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func newService() (*Service, *memStore) {
	store := &memStore{rows: map[string]domain.Appointment{}, partners: map[string]PartnerBooking{}}
	var seq atomic.Int64
	return &Service{Store: store, Directory: seedDirectory{}, Now: func() time.Time { return fixedNow },
		NewID: func() string { return fmt.Sprintf("id-%d", seq.Add(1)) }}, store
}

func TestBookWritesAppointmentAndEventAtomically(t *testing.T) {
	svc, store := newService()
	ctx := obs.WithCorrelationID(context.Background(), "corr-7")
	a, err := svc.Book(ctx, "p-1", "d-1", fixedNow.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.outbox) != 1 || store.outbox[0].RoutingKey != "turnos.reservado.v1" {
		t.Fatalf("expected one booked event, got %+v", store.outbox)
	}
	var env map[string]any
	if err := json.Unmarshal(store.outbox[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if env["correlationId"] != "corr-7" || env["version"] != float64(1) || data["turnId"] != a.ID || data["appointmentVersion"] != float64(1) {
		t.Fatalf("envelope does not follow the schema: %v", env)
	}
	if store.outbox[0].Headers["x-correlation-id"] != "corr-7" {
		t.Fatalf("correlation not stored for the relay: %v", store.outbox[0].Headers)
	}
}

func TestRejectedBookingLeavesNoRowOrEvent(t *testing.T) {
	svc, store := newService()
	_, err := svc.Book(context.Background(), "p-1", "d-1", fixedNow.Add(2*time.Hour))
	if !errors.Is(err, domain.ErrLeadTime) || len(store.rows) != 0 || len(store.outbox) != 0 {
		t.Fatalf("want lead time rejection with no side effects, got err=%v rows=%d outbox=%d", err, len(store.rows), len(store.outbox))
	}
}

func TestConcurrentBookingsOfSameSlotKeepOnlyOne(t *testing.T) {
	svc, store := newService()
	start := fixedNow.Add(72 * time.Hour)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := svc.Book(context.Background(), fmt.Sprintf("p-%d", i), "d-1", start); err == nil {
				ok.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 1 || len(store.rows) != 1 || len(store.outbox) != 1 {
		t.Fatalf("want exactly one booking, got ok=%d rows=%d outbox=%d", ok.Load(), len(store.rows), len(store.outbox))
	}
}

func TestCancelEmitsEventWithReasonCode(t *testing.T) {
	svc, store := newService()
	a, _ := svc.Book(context.Background(), "p-1", "d-1", fixedNow.Add(48*time.Hour))
	c, err := svc.Cancel(context.Background(), a.ID, "")
	if err != nil || c.Status != domain.StatusCancelled {
		t.Fatalf("cancel failed: %v", err)
	}
	var env struct {
		Data struct {
			ReasonCode         string `json:"reasonCode"`
			AppointmentVersion int    `json:"appointmentVersion"`
		} `json:"data"`
	}
	_ = json.Unmarshal(store.outbox[1].Payload, &env)
	if store.outbox[1].RoutingKey != "turnos.cancelado.v1" || env.Data.ReasonCode == "" || env.Data.AppointmentVersion != 2 {
		t.Fatalf("cancel event invalid: %s", store.outbox[1].Payload)
	}
}

func TestUnknownDoctorIsReported(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Book(context.Background(), "p-1", "nope", fixedNow.Add(48*time.Hour)); !errors.Is(err, ErrUnknownDoctor) {
		t.Fatalf("want ErrUnknownDoctor, got %v", err)
	}
}

func (s *memStore) AppointmentsPage(ctx context.Context, patient, doctor string, page int, now time.Time) ([]domain.Appointment, bool, error) {
	var list []domain.Appointment
	var err error
	if doctor != "" {
		list, err = s.ListByDoctor(ctx, doctor)
	} else {
		list, err = s.ListByPatient(ctx, patient)
	}
	if err != nil {
		return nil, false, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].StartAt.Before(list[j].StartAt) })
	start := (page - 1) * PageSize
	if start >= len(list) {
		return []domain.Appointment{}, false, nil
	}
	end := min(start+PageSize, len(list))
	return list[start:end], end < len(list), nil
}

func (s *memStore) HasRelationship(ctx context.Context, doctor, patient string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.rows {
		if a.DoctorID == doctor && a.PatientID == patient && a.Status == domain.StatusBooked {
			return true, nil
		}
	}
	return false, nil
}
