//go:build integration

// Integration tests against a real MySQL. Requires Docker:
// go test -tags integration ./services/medicos/...
package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mysql"

	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

func startDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("medicos"), mysql.WithUsername("medicos"), mysql.WithPassword("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func count(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Restarting the service re-applies schema and seed without duplicating rows
// or overwriting edits made by an admin.
func TestSeedIsIdempotentAndKeepsEdits(t *testing.T) {
	ctx := context.Background()
	dsn := startDSN(t)
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	svc := &app.Service{Store: s, Now: time.Now, NewID: func() string { return "11111111-1111-4111-8111-111111111111" }}
	if _, err := svc.UpdateProfile(ctx, "doc-001", domain.Profile{FullName: "Laura B. Gómez", Specialties: []string{"cardiologia"}, Active: true}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, s2, "SELECT COUNT(*) FROM doctors"); n != len(SeedDoctors) {
		t.Fatalf("want %d doctors, got %d", len(SeedDoctors), n)
	}
	d, err := s2.Doctor(ctx, "doc-001")
	if err != nil || d.FullName != "Laura B. Gómez" || d.ProfileVersion != 2 || len(d.Specialties) != 1 {
		t.Fatalf("edit lost after restart: %+v %v", d, err)
	}
	sp, _ := s2.Specialties(ctx)
	if len(sp) != len(SeedSpecialties) {
		t.Fatalf("specialties %v", sp)
	}
}

// The profile change and its event commit together; a rejected change leaves neither.
func TestUpdateProfileWritesOutboxInSameTransaction(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, startDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	svc := &app.Service{Store: s, Now: time.Now, NewID: func() string { return "22222222-2222-4222-8222-222222222222" }}
	if _, err := svc.UpdateProfile(ctx, "doc-002", domain.Profile{FullName: "M. Pereyra", Specialties: []string{"astrologia"}, Active: true}); !errors.Is(err, app.ErrUnknownSpecialty) {
		t.Fatalf("want ErrUnknownSpecialty, got %v", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM outbox"); n != 0 {
		t.Fatalf("rejected update wrote %d events", n)
	}
	d, err := svc.UpdateProfile(ctx, "doc-002", domain.Profile{FullName: "M. Pereyra", Specialties: []string{"pediatria", "dermatologia"}, AcceptsMinors: true, Active: true})
	if err != nil || d.ProfileVersion != 2 {
		t.Fatalf("update: %+v %v", d, err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM outbox WHERE routing_key = 'medicos.perfil-actualizado.v1' AND published_at IS NULL"); n != 1 {
		t.Fatalf("want one pending event, got %d", n)
	}
	got, _ := s.Doctor(ctx, "doc-002")
	if got.Specialties[0] != "pediatria" || got.Specialties[1] != "dermatologia" {
		t.Fatalf("specialty order lost: %v", got.Specialties)
	}
}

// Redelivered and out-of-order Turnos events converge to the highest version.
func TestApplyBookingKeepsHighestVersion(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, startDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 12, 3, 10, 0, 0, 0, time.UTC)
	booked := app.Booking{TurnID: "t-1", DoctorID: "doc-001", Start: start, End: start.Add(30 * time.Minute), Active: true, Version: 1}
	cancelled := booked
	cancelled.Active, cancelled.Version = false, 2

	for _, step := range []struct {
		name    string
		b       app.Booking
		changed bool
		busy    int
	}{
		{"cancel arrives first", cancelled, true, 0},
		{"older booking ignored", booked, false, 0},
		{"duplicate cancel ignored", cancelled, false, 0},
	} {
		changed, err := s.ApplyBooking(ctx, step.b)
		if err != nil || changed != step.changed {
			t.Fatalf("%s: changed=%v err=%v", step.name, changed, err)
		}
		busy, _ := s.BusyIntervals(ctx, "doc-001", start.Add(-time.Hour))
		if len(busy) != step.busy {
			t.Fatalf("%s: busy=%v", step.name, busy)
		}
	}
	other := app.Booking{TurnID: "t-2", DoctorID: "doc-001", Start: start, End: start.Add(30 * time.Minute), Active: true, Version: 1}
	if changed, _ := s.ApplyBooking(ctx, other); !changed {
		t.Fatal("new booking must change the projection")
	}
	busy, _ := s.BusyIntervals(ctx, "doc-001", start.Add(-time.Hour))
	if len(busy) != 1 || !busy[0].Start.Equal(start) {
		t.Fatalf("busy=%v", busy)
	}
}
