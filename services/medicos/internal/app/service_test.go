package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

var now = time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)

type fakeStore struct {
	doctors     map[string]domain.Doctor
	specialties []domain.Specialty
	outbox      []OutboxMessage
	bookings    map[string]Booking
	failSave    bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		doctors: map[string]domain.Doctor{
			"doc-001": {ID: "doc-001", FullName: "Laura Gomez", Specialties: []string{"clinica-medica"}, Active: true, ProfileVersion: 1},
		},
		specialties: []domain.Specialty{{Code: "clinica-medica", Name: "Clinica medica"}, {Code: "pediatria", Name: "Pediatria"}},
		bookings:    map[string]Booking{},
	}
}

type fakeTx struct {
	s       *fakeStore
	doctors map[string]domain.Doctor
	outbox  []OutboxMessage
}

func (t *fakeTx) GetForUpdate(_ context.Context, id string) (domain.Doctor, error) {
	d, ok := t.doctors[id]
	if !ok {
		return d, ErrNotFound
	}
	return d, nil
}
func (t *fakeTx) SpecialtiesExist(_ context.Context, codes []string) (bool, error) {
	for _, c := range codes {
		if !slices.ContainsFunc(t.s.specialties, func(s domain.Specialty) bool { return s.Code == c }) {
			return false, nil
		}
	}
	return true, nil
}
func (t *fakeTx) SaveProfile(_ context.Context, d domain.Doctor) error {
	t.doctors[d.ID] = d
	return nil
}
func (t *fakeTx) AppendOutbox(_ context.Context, m OutboxMessage) error {
	t.outbox = append(t.outbox, m)
	return nil
}

// InTx only applies the changes when fn and the commit succeed, like a real transaction.
func (s *fakeStore) InTx(ctx context.Context, fn func(Tx) error) error {
	tx := &fakeTx{s: s, doctors: map[string]domain.Doctor{}}
	for k, v := range s.doctors {
		tx.doctors[k] = v
	}
	if err := fn(tx); err != nil {
		return err
	}
	if s.failSave {
		return errors.New("commit failed")
	}
	s.doctors, s.outbox = tx.doctors, append(s.outbox, tx.outbox...)
	return nil
}
func (s *fakeStore) Doctor(_ context.Context, id string) (domain.Doctor, error) {
	d, ok := s.doctors[id]
	if !ok {
		return d, ErrNotFound
	}
	return d, nil
}
func (s *fakeStore) Doctors(context.Context) ([]domain.Doctor, error) {
	var out []domain.Doctor
	for _, d := range s.doctors {
		out = append(out, d)
	}
	return out, nil
}
func (s *fakeStore) Specialties(context.Context) ([]domain.Specialty, error) {
	return s.specialties, nil
}
func (s *fakeStore) ApplyBooking(_ context.Context, b Booking) (bool, error) {
	if old, ok := s.bookings[b.TurnID]; ok && old.Version >= b.Version {
		return false, nil
	}
	s.bookings[b.TurnID] = b
	return true, nil
}
func (s *fakeStore) BusyIntervals(_ context.Context, doctorID string, from time.Time) ([]domain.Interval, error) {
	var out []domain.Interval
	for _, b := range s.bookings {
		if b.DoctorID == doctorID && b.Active && b.End.After(from) {
			out = append(out, domain.Interval{Start: b.Start, End: b.End})
		}
	}
	return out, nil
}

type fakeIndex struct {
	upserts []domain.SearchDoc
	rebuilt []domain.SearchDoc
	err     error
	lastQ   domain.Query
}

func (f *fakeIndex) Upsert(_ context.Context, d domain.SearchDoc) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, d)
	return nil
}
func (f *fakeIndex) Search(_ context.Context, q domain.Query) (domain.Page, error) {
	f.lastQ = q
	return domain.Page{}, f.err
}
func (f *fakeIndex) Rebuild(_ context.Context, docs []domain.SearchDoc) (string, error) {
	f.rebuilt = docs
	return "doctors-v2", f.err
}

func newService() (*Service, *fakeStore, *fakeIndex) {
	st, ix := newFakeStore(), &fakeIndex{}
	return &Service{Store: st, Index: ix, Now: func() time.Time { return now }, NewID: func() string { return "evt-1" }}, st, ix
}

func TestUpdateProfileSavesNewVersionAndEventTogether(t *testing.T) {
	svc, st, _ := newService()
	ctx := obs.WithCorrelationID(context.Background(), "corr-1")
	d, err := svc.UpdateProfile(ctx, "doc-001", domain.Profile{FullName: "Laura B. Gomez", Specialties: []string{"pediatria"}, AcceptsMinors: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.ProfileVersion != 2 || st.doctors["doc-001"].FullName != "Laura B. Gomez" {
		t.Fatalf("profile not saved with version 2: %+v", st.doctors["doc-001"])
	}
	if len(st.outbox) != 1 || st.outbox[0].RoutingKey != RoutingProfileUpdated || st.outbox[0].Headers["x-correlation-id"] != "corr-1" {
		t.Fatalf("want one %s outbox event, got %+v", RoutingProfileUpdated, st.outbox)
	}
	var env struct {
		EventID, EventType, OccurredAt, CorrelationID string
		Version                                       int
		Data                                          struct {
			DoctorID       string   `json:"doctorId"`
			FullName       string   `json:"fullName"`
			Specialties    []string `json:"specialties"`
			AcceptsMinors  bool     `json:"acceptsMinors"`
			Active         bool     `json:"active"`
			ProfileVersion int64    `json:"profileVersion"`
		}
	}
	if err := json.Unmarshal(st.outbox[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	if env.EventID != "evt-1" || env.EventType != RoutingProfileUpdated || env.Version != 1 || env.OccurredAt != "2026-12-01T10:00:00Z" ||
		env.CorrelationID != "corr-1" || env.Data.DoctorID != "doc-001" || env.Data.ProfileVersion != 2 || !env.Data.AcceptsMinors ||
		!slices.Equal(env.Data.Specialties, []string{"pediatria"}) {
		t.Fatalf("unexpected envelope %s", st.outbox[0].Payload)
	}
}

func TestUpdateProfileRejections(t *testing.T) {
	valid := domain.Profile{FullName: "X", Specialties: []string{"pediatria"}, Active: true}
	tests := []struct {
		name    string
		id      string
		profile domain.Profile
		check   func(error) bool
	}{
		{"unknown doctor", "doc-999", valid, func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"unknown specialty", "doc-001", domain.Profile{FullName: "X", Specialties: []string{"astrologia"}}, func(err error) bool { return errors.Is(err, ErrUnknownSpecialty) }},
		{"invalid profile", "doc-001", domain.Profile{FullName: ""}, func(err error) bool {
			var inv *domain.InvalidError
			return errors.As(err, &inv)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st, _ := newService()
			_, err := svc.UpdateProfile(context.Background(), tt.id, tt.profile)
			if !tt.check(err) {
				t.Fatalf("unexpected error %v", err)
			}
			if len(st.outbox) != 0 || st.doctors["doc-001"].ProfileVersion != 1 {
				t.Fatal("rejected update must not change the profile or emit events")
			}
		})
	}
}

func TestIndexDoctorUsesMySQLStateAndNextAvailable(t *testing.T) {
	svc, st, ix := newService()
	from := now.Add(domain.MinLeadTime)
	st.bookings["t-1"] = Booking{TurnID: "t-1", DoctorID: "doc-001", Start: from, End: from.Add(30 * time.Minute), Active: true, Version: 1}
	if err := svc.IndexDoctor(context.Background(), "doc-001"); err != nil {
		t.Fatal(err)
	}
	if len(ix.upserts) != 1 {
		t.Fatalf("want one upsert, got %d", len(ix.upserts))
	}
	got := ix.upserts[0]
	if got.DoctorID != "doc-001" || got.ProfileVersion != 1 || got.NextAvailableAt == nil || !got.NextAvailableAt.Equal(from.Add(30*time.Minute)) {
		t.Fatalf("unexpected doc %+v", got)
	}
}

func TestHandleBookingIsIdempotent(t *testing.T) {
	svc, _, ix := newService()
	b := Booking{TurnID: "t-1", DoctorID: "doc-001", Start: now.Add(48 * time.Hour), End: now.Add(49 * time.Hour), Active: true, Version: 1}
	dup, err := svc.HandleBooking(context.Background(), b)
	if err != nil || dup {
		t.Fatalf("first delivery: dup=%v err=%v", dup, err)
	}
	dup, err = svc.HandleBooking(context.Background(), b)
	if err != nil || !dup {
		t.Fatalf("second delivery must be a duplicate: dup=%v err=%v", dup, err)
	}
	if len(ix.upserts) != 2 {
		t.Fatalf("duplicate must repair the derived index, got %d upserts", len(ix.upserts))
	}
	if _, err := svc.HandleBooking(context.Background(), Booking{TurnID: "t-2", DoctorID: "doc-999", Version: 1}); !errors.Is(err, ErrUnknownDoctor) {
		t.Fatalf("unknown doctor: want ErrUnknownDoctor, got %v", err)
	}
}

func TestSearchValidatesBeforeQueryingIndex(t *testing.T) {
	svc, _, ix := newService()
	_, err := svc.Search(context.Background(), domain.Query{Page: 21, Size: 50})
	var inv *domain.InvalidError
	if !errors.As(err, &inv) || ix.lastQ.Size != 0 {
		t.Fatalf("want validation error without index call, got %v", err)
	}
	ix.err = ErrSearchUnavailable
	if _, err := svc.Search(context.Background(), domain.Query{Page: 1, Size: 10}); !errors.Is(err, ErrSearchUnavailable) {
		t.Fatalf("want ErrSearchUnavailable, got %v", err)
	}
}

func TestReindexBuildsEveryDoctorFromMySQL(t *testing.T) {
	svc, st, ix := newService()
	st.doctors["doc-002"] = domain.Doctor{ID: "doc-002", FullName: "Martin Pereyra", Specialties: []string{"pediatria"}, Active: true, ProfileVersion: 3}
	name, n, err := svc.Reindex(context.Background())
	if err != nil || name != "doctors-v2" || n != 2 || len(ix.rebuilt) != 2 {
		t.Fatalf("name=%s n=%d err=%v docs=%d", name, n, err, len(ix.rebuilt))
	}
	for _, d := range ix.rebuilt {
		if d.NextAvailableAt == nil || !d.NextAvailableAt.Equal(now.Add(domain.MinLeadTime)) {
			t.Fatalf("nextAvailableAt not computed for %+v", d)
		}
	}
}

func TestBookingRetryRepairsIndexAfterSQLCommit(t *testing.T) {
	svc, _, ix := newService()
	b := Booking{TurnID: "retry-1", DoctorID: "doc-001", Start: now.Add(48 * time.Hour), End: now.Add(49 * time.Hour), Active: true, Version: 1}
	ix.err = ErrSearchUnavailable
	if _, err := svc.HandleBooking(context.Background(), b); err == nil {
		t.Fatal("expected outage")
	}
	ix.err = nil
	if duplicate, err := svc.HandleBooking(context.Background(), b); err != nil || !duplicate || len(ix.upserts) != 1 {
		t.Fatalf("index was not repaired: duplicate=%v err=%v docs=%d", duplicate, err, len(ix.upserts))
	}
}
