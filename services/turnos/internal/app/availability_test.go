package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// recordingCache records the days the service invalidates after a commit.
type recordingCache struct {
	mu   sync.Mutex
	keys []string
}

func (c *recordingCache) Invalidate(_ context.Context, doctorID, day string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, doctorID+"/"+day)
}

// fixedNow is Tuesday 2026-10-20 09:00 in the clinic zone; Thursday 22 is bookable.
var thursday = time.Date(2026, 10, 22, 0, 0, 0, 0, domain.ClinicZone)

func TestAvailabilityListsFreeSlotsOfTheDay(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Book(context.Background(), "p-1", "d-1", thursday.Add(9*time.Hour+30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	av, err := svc.Availability(context.Background(), "d-1", thursday)
	if err != nil {
		t.Fatal(err)
	}
	if av.DoctorID != "d-1" || av.Date != "2026-10-22" || av.Duration != 30*time.Minute || len(av.Slots) != 3 {
		t.Fatalf("unexpected availability: %+v", av)
	}
	for _, s := range av.Slots {
		if s.StartAt.Format("15:04") == "09:30" {
			t.Fatalf("booked slot listed as free: %+v", av.Slots)
		}
	}
}

func TestAvailabilitySlotIsBookableByAnotherPatient(t *testing.T) {
	svc, _ := newService()
	av, _ := svc.Availability(context.Background(), "d-1", thursday)
	if _, err := svc.Book(context.Background(), "p-2", "d-1", av.Slots[0].StartAt); err != nil {
		t.Fatalf("free slot rejected: %v", err)
	}
}

func TestAvailabilityOfUnknownDoctor(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Availability(context.Background(), "nope", thursday); !errors.Is(err, ErrUnknownDoctor) {
		t.Fatalf("want ErrUnknownDoctor, got %v", err)
	}
}

func TestBookingAndCancellationInvalidateTheDay(t *testing.T) {
	svc, _ := newService()
	cache := &recordingCache{}
	svc.Cache = cache
	a, err := svc.Book(context.Background(), "p-1", "d-1", thursday.Add(10*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(context.Background(), a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(cache.keys) != 2 || cache.keys[0] != "d-1/2026-10-22" || cache.keys[1] != "d-1/2026-10-22" {
		t.Fatalf("want two invalidations of d-1/2026-10-22, got %v", cache.keys)
	}
}

func TestPartnerBookingAndCancellationInvalidateTheDay(t *testing.T) {
	svc, _ := newPartnerService()
	cache := &recordingCache{}
	svc.Cache = cache
	a, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", thursday.Add(10*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// A replay changes nothing and does not invalidate again.
	_, _, _ = svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", thursday.Add(10*time.Hour))
	if _, err := svc.CancelForPartner(context.Background(), "carcruds", a.ID); err != nil {
		t.Fatal(err)
	}
	if len(cache.keys) != 2 {
		t.Fatalf("want booking and cancellation invalidations, got %v", cache.keys)
	}
}

func TestRejectedBookingDoesNotInvalidate(t *testing.T) {
	svc, _ := newService()
	cache := &recordingCache{}
	svc.Cache = cache
	_, _ = svc.Book(context.Background(), "p-1", "d-1", fixedNow.Add(time.Hour))
	if len(cache.keys) != 0 {
		t.Fatalf("rejected booking invalidated %v", cache.keys)
	}
}

func TestNextAvailableSkipsBookedSlotsAndRespectsSchedule(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	first, err := svc.NextAvailable(ctx, "d-1")
	if err != nil || first == nil {
		t.Fatalf("want a slot, got %v %v", first, err)
	}
	doctor, _ := svc.Directory.Doctor(ctx, "d-1")
	if first.StartAt.Sub(fixedNow) < domain.MinLeadTime || !doctor.Schedule.Allows(first.StartAt, doctor.Duration) {
		t.Fatalf("next slot is not bookable: %s", first.StartAt)
	}
	if _, err := svc.Book(ctx, "p-1", "d-1", first.StartAt); err != nil {
		t.Fatal(err)
	}
	second, err := svc.NextAvailable(ctx, "d-1")
	if err != nil || second == nil || !second.StartAt.After(first.StartAt) {
		t.Fatalf("booked slot still offered: first %s, second %v, err %v", first.StartAt, second, err)
	}
}

func TestNextAvailableReportsUnknownDoctor(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.NextAvailable(context.Background(), "nope"); !errors.Is(err, ErrUnknownDoctor) {
		t.Fatalf("want ErrUnknownDoctor, got %v", err)
	}
}

// Regression: search showed 00:37 for a doctor who works 9 to 11, because the
// value ignored office hours. The first slot must be inside the schedule.
func TestNextAvailableAtMidnightStartsAtOpeningTime(t *testing.T) {
	svc, _ := newService()
	svc.Now = func() time.Time { return time.Date(2026, 10, 20, 0, 37, 0, 0, domain.ClinicZone) }
	slot, err := svc.NextAvailable(context.Background(), "d-1")
	if err != nil || slot == nil {
		t.Fatalf("want a slot, got %v %v", slot, err)
	}
	if got := slot.StartAt.In(domain.ClinicZone).Format("2006-01-02 15:04"); got != "2026-10-21 09:00" {
		t.Fatalf("want 2026-10-21 09:00, got %s", got)
	}
}
