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
