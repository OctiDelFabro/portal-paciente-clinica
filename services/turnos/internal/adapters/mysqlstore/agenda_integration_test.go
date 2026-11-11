//go:build integration

package mysqlstore

import (
	"context"
	"errors"
	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/turnos/internal/adapters/directory/fixture"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"sync"
	"testing"
	"time"
)

func TestPersistentAgendaAtomicRescheduleBlocksAndReminderReplicas(t *testing.T) {
	store := startStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	start := nextWorkday(now).Add(9 * time.Hour)
	svc := &app.Service{Store: store, Directory: fixture.Directory{}, Now: func() time.Time { return now }, NewID: obs.NewID}
	a, err := svc.Book(ctx, "pat-001", "doc-001", start)
	if err != nil {
		t.Fatal(err)
	}
	agenda, err := svc.Schedule(ctx, "doc-001")
	if err != nil {
		t.Fatal(err)
	}
	agenda.Duration = 45 * time.Minute
	if err := svc.SetSchedule(ctx, "doc-001", agenda); err != nil {
		t.Fatal(err)
	}
	saved, err := store.SavedAgenda(ctx, "doc-001")
	if err != nil || saved.Duration != 45*time.Minute {
		t.Fatal("agenda not durable", err)
	}
	got, _ := store.Get(ctx, a.ID)
	if got.EndAt.Sub(got.StartAt) != 30*time.Minute {
		t.Fatal("old reservation duration mutated")
	}
	bad := agenda
	bad.Schedule.Open = 10 * time.Hour
	if err := svc.SetSchedule(ctx, "doc-001", bad); !errors.Is(err, domain.ErrAgendaConflict) {
		t.Fatalf("agenda hid booked appointment: %v", err)
	}
	occupied, err := svc.Book(ctx, "pat-003", "doc-001", start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reschedule(ctx, "patient", "pat-001", a.ID, occupied.StartAt); !errors.Is(err, domain.ErrOverlap) {
		t.Fatalf("expected conflict: %v", err)
	}
	got, _ = store.Get(ctx, a.ID)
	if got.Status != domain.StatusBooked {
		t.Fatal("rollback lost original")
	}
	moved, err := svc.Reschedule(ctx, "patient", "pat-001", a.ID, start.AddDate(0, 0, 7))
	if err != nil {
		t.Fatal(err)
	}
	if moved.EndAt.Sub(moved.StartAt) != 45*time.Minute {
		t.Fatal("new duration ignored")
	}
	// Both filters must apply together, otherwise a patient could read another person's appointments.
	list, _, err := store.AppointmentsPage(ctx, "pat-001", "doc-001", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.PatientID != "pat-001" || a.DoctorID != "doc-001" {
			t.Fatal("cross-scope disclosure")
		}
	}
	// Ordinary cancellation is too late, while a medical block is the explicit exception.
	now = occupied.StartAt.Add(-time.Hour)
	if _, err := svc.Cancel(ctx, occupied.ID, ""); err == nil {
		t.Fatal("ordinary late cancellation allowed")
	}
	b, err := svc.BlockAgenda(ctx, "doc-001", occupied.StartAt, occupied.EndAt, "Ausencia del profesional")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(ctx, occupied.ID)
	if got.Status != domain.StatusCancelled {
		t.Fatal("block did not cancel")
	}
	// Another replica sees persisted blocks; unlocking never resurrects a cancelled turn.
	replica := *svc
	blocks, err := replica.ListBlocks(ctx, "doc-001")
	if err != nil || len(blocks) != 1 {
		t.Fatalf("block not visible: %+v %v", blocks, err)
	}
	now = occupied.StartAt.Add(-48 * time.Hour)
	if _, err := replica.Book(ctx, "pat-003", "doc-001", occupied.StartAt); !errors.Is(err, domain.ErrOverlap) {
		t.Fatalf("block accepted booking: %v", err)
	}
	if err := replica.ReleaseBlock(ctx, "doc-001", b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(ctx, occupied.ID)
	if got.Status != domain.StatusCancelled {
		t.Fatal("unblock resurrected appointment")
	}
	now = moved.StartAt.Add(-23 * time.Hour)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := replica.QueueReminders(ctx, 200); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var reminders int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE routing_key='turnos.recordatorio.v1'`).Scan(&reminders); err != nil {
		t.Fatal(err)
	}
	if reminders != 1 {
		t.Fatalf("two replicas emitted %d reminders", reminders)
	}
	if err := svc.QueueReminders(ctx, 200); err != nil {
		t.Fatal(err)
	}
	var markers int
	_ = store.DB.QueryRow(`SELECT COUNT(*) FROM appointment_reminders`).Scan(&markers)
	if markers != 1 {
		t.Fatalf("wrong markers: %d", markers)
	}
}

func TestConcurrentBookingVersusBlockHasNoActiveOverlap(t *testing.T) {
	store := startStore(t)
	ctx := context.Background()
	now := time.Now()
	start := nextWorkday(now).Add(9 * time.Hour)
	svc := &app.Service{Store: store, Directory: fixture.Directory{}, Now: func() time.Time { return now }, NewID: obs.NewID}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Book(ctx, "pat-001", "doc-001", start)
		if err != nil && !errors.Is(err, domain.ErrOverlap) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := svc.BlockAgenda(ctx, "doc-001", start, start.Add(time.Hour), "Guardia"); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	list, err := store.ActiveForDoctor(ctx, "doc-001", start, start.Add(time.Hour))
	if err != nil || len(list) != 0 {
		t.Fatalf("booked visit inside committed block: %+v %v", list, err)
	}
}
