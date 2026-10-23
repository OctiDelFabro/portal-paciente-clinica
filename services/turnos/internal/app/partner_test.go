package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

func (s *memStore) PartnerBookingByKey(_ context.Context, consumer, key string) (PartnerBooking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return memTx{s}.FindPartnerBooking(context.Background(), consumer, key)
}

func (s *memStore) PartnerBookingOf(_ context.Context, appointmentID string) (PartnerBooking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pb, ok := s.partners[appointmentID]
	if !ok {
		return pb, ErrNotFound
	}
	return pb, nil
}

func (s *memStore) PartnerUsage(_ context.Context, consumer string, from, to time.Time) ([]UsageItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []UsageItem
	for _, pb := range s.partners {
		if pb.Consumer == consumer && !pb.BookedAt.Before(from) && pb.BookedAt.Before(to) {
			out = append(out, UsageItem{AppointmentID: pb.AppointmentID, BookedAt: pb.BookedAt,
				PriceCents: pb.PriceCents, Currency: pb.Currency, Status: s.rows[pb.AppointmentID].Status})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppointmentID < out[j].AppointmentID })
	return out, nil
}

func (t memTx) FindPartnerBooking(_ context.Context, consumer, key string) (PartnerBooking, error) {
	for _, pb := range t.s.partners {
		if pb.Consumer == consumer && pb.IdempotencyKey == key {
			return pb, nil
		}
	}
	return PartnerBooking{}, ErrNotFound
}

func (t memTx) InsertPartnerBooking(_ context.Context, pb PartnerBooking) error {
	t.s.partners[pb.AppointmentID] = pb
	return nil
}

func newPartnerService() (*Service, *memStore) {
	svc, store := newService()
	svc.Price = Pricing{Cents: 2500000, Currency: "ARS"}
	return svc, store
}

var slot = fixedNow.Add(48 * time.Hour)

func TestPartnerRepeatWithSameKeyReturnsSameAppointment(t *testing.T) {
	svc, store := newPartnerService()
	first, replayed, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot)
	if err != nil || replayed {
		t.Fatalf("first booking: replayed=%v err=%v", replayed, err)
	}
	// Mismo instante expresado en otra zona horaria: son los mismos datos.
	again, replayed, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot.In(time.FixedZone("ART", -3*3600)))
	if err != nil || !replayed || again.ID != first.ID {
		t.Fatalf("repeat: id=%s replayed=%v err=%v, want %s replayed", again.ID, replayed, err, first.ID)
	}
	if len(store.rows) != 1 || len(store.outbox) != 1 || len(store.partners) != 1 {
		t.Fatalf("repeat must not create effects: rows=%d outbox=%d partners=%d", len(store.rows), len(store.outbox), len(store.partners))
	}
}

func TestPartnerSameKeyWithOtherDataIsConflict(t *testing.T) {
	svc, store := newPartnerService()
	if _, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot.Add(time.Hour))
	if !errors.Is(err, ErrIdempotencyConflict) || len(store.rows) != 1 {
		t.Fatalf("want conflict and no new row, got err=%v rows=%d", err, len(store.rows))
	}
}

func TestPartnerConcurrentRequestsWithOneKeyCreateOneAppointment(t *testing.T) {
	svc, store := newPartnerService()
	var wg sync.WaitGroup
	ids := make([]string, 20)
	errs := make([]error, 20)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot)
			ids[i], errs[i] = a.ID, err
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("request %d: id=%s err=%v, want %s", i, ids[i], errs[i], ids[0])
		}
	}
	if len(store.rows) != 1 || len(store.partners) != 1 {
		t.Fatalf("rows=%d partners=%d, want 1", len(store.rows), len(store.partners))
	}
}

func TestPartnerKeysAreScopedPerConsumer(t *testing.T) {
	svc, _ := newPartnerService()
	a, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot)
	if err != nil {
		t.Fatal(err)
	}
	b, replayed, err := svc.BookForPartner(context.Background(), "other", "k-1", "p-2", "d-1", slot.Add(time.Hour))
	if err != nil || replayed || b.ID == a.ID {
		t.Fatalf("other consumer must get its own booking: id=%s replayed=%v err=%v", b.ID, replayed, err)
	}
}

func TestPartnerRejectedBookingDoesNotReserveTheKey(t *testing.T) {
	svc, store := newPartnerService()
	if _, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", fixedNow.Add(time.Hour)); !errors.Is(err, domain.ErrLeadTime) {
		t.Fatalf("want lead time rejection, got %v", err)
	}
	if len(store.partners) != 0 {
		t.Fatal("rejection must not be billed nor stored under the key")
	}
	if _, replayed, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot); err != nil || replayed {
		t.Fatalf("corrected request with the same key: replayed=%v err=%v", replayed, err)
	}
}

func TestPartnerOnlySeesAndCancelsOwnAppointments(t *testing.T) {
	svc, _ := newPartnerService()
	a, _, _ := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot)
	portal, _ := svc.Book(context.Background(), "p-9", "d-1", slot.Add(2*time.Hour))
	for _, id := range []string{portal.ID, "missing"} {
		if _, err := svc.GetForPartner(context.Background(), "carcruds", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetForPartner(%s) = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := svc.GetForPartner(context.Background(), "other", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other consumer read: %v", err)
	}
	if _, err := svc.CancelForPartner(context.Background(), "other", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other consumer cancel: %v", err)
	}
	c, err := svc.CancelForPartner(context.Background(), "carcruds", a.ID)
	if err != nil || c.Status != domain.StatusCancelled || c.CancelReason != "partner_request" {
		t.Fatalf("own cancel: %+v %v", c, err)
	}
}

func TestPartnerUsageChargesEachBookingAtItsOwnPrice(t *testing.T) {
	svc, _ := newPartnerService()
	ctx := context.Background()
	a, _, _ := svc.BookForPartner(ctx, "carcruds", "k-1", "p-1", "d-1", slot)
	_, _, _ = svc.BookForPartner(ctx, "carcruds", "k-1", "p-1", "d-1", slot) // repeticion: no se cobra
	svc.Price = Pricing{Cents: 3000000, Currency: "ARS"}
	_, _, _ = svc.BookForPartner(ctx, "carcruds", "k-2", "p-2", "d-1", slot.Add(time.Hour))
	_, _, _ = svc.BookForPartner(ctx, "other", "k-1", "p-3", "d-1", slot.Add(2*time.Hour))
	if _, err := svc.CancelForPartner(ctx, "carcruds", a.ID); err != nil {
		t.Fatal(err)
	}
	u, err := svc.Usage(ctx, "carcruds", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Items) != 2 || u.Items[0].Status != domain.StatusCancelled {
		t.Fatalf("items = %+v", u.Items)
	}
	want := []UsageTotal{{Currency: "ARS", Bookings: 2, AmountCents: 5500000}}
	if fmt.Sprint(u.Totals) != fmt.Sprint(want) || !u.To.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("totals=%+v to=%s, want %+v", u.Totals, u.To, want)
	}
}

func TestDoctorsListsTheDirectory(t *testing.T) {
	svc, _ := newPartnerService()
	ds, err := svc.Doctors(context.Background())
	if err != nil || len(ds) != 1 || ds[0].ID != "d-1" {
		t.Fatalf("doctors = %+v, %v", ds, err)
	}
}
