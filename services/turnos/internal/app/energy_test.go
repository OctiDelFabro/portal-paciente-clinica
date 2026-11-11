package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

func (s *memStore) SaveEnergy(_ context.Context, id string, e domain.Energy, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[id]
	if !ok {
		return ErrNotFound
	}
	if e.Status == domain.EnergyEstimated && a.Energy.Status == domain.EnergyPending {
		a.Energy = e
		s.rows[id] = a
	}
	return nil
}

func (s *memStore) PendingEnergy(_ context.Context, now time.Time, limit int) ([]domain.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Appointment
	for _, a := range s.rows {
		if a.Energy.Status == domain.EnergyPending && a.Status == domain.StatusBooked && a.StartAt.After(now) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fakeEstimator answers kWh or err and records calls. It fails the test if it
// is called while a store transaction (and so the booking lock) is held.
type fakeEstimator struct {
	t        *testing.T
	store    *memStore
	microKWh int64
	err      error
	calls    []domain.Consumption
	deadline time.Duration
}

func (f *fakeEstimator) Estimate(ctx context.Context, c domain.Consumption) (EnergyEstimate, error) {
	if f.store != nil {
		if !f.store.mu.TryLock() {
			f.t.Error("provider called while the booking transaction holds the lock")
		} else {
			f.store.mu.Unlock()
		}
	}
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	f.calls = append(f.calls, c)
	if f.err != nil {
		return EnergyEstimate{}, f.err
	}
	return EnergyEstimate{MicroKWh: f.microKWh, AlgorithmVersion: "consumo-v1"}, nil
}

var testTariff = domain.Tariff{BaseCents: 2_500_000, CentsPerKWh: 18_000}

func newEnergyService(t *testing.T, est *fakeEstimator) (*Service, *memStore) {
	svc, store := newService()
	est.t, est.store = t, store
	svc.Energy, svc.Tariff = est, testTariff
	return svc, store
}

func TestQuoteAddsEstimatedEnergyCostToBasePrice(t *testing.T) {
	est := &fakeEstimator{microKWh: 381_000}
	svc, _ := newEnergyService(t, est)
	q, err := svc.Quote(context.Background(), "d-1", slot)
	if err != nil {
		t.Fatal(err)
	}
	p := q.Price
	if q.DoctorID != "d-1" || !q.StartAt.Equal(slot) || p.BaseCents != 2_500_000 || p.Energy.Status != domain.EnergyEstimated ||
		p.Energy.MicroKWh != 381_000 || p.Energy.CostCents != 6858 || p.TotalCents != 2_506_858 {
		t.Fatalf("got %+v", q)
	}
	if len(est.calls) != 1 || est.calls[0].Hours != 0.5 {
		t.Fatalf("want one estimate of the 30-minute slot, got %+v", est.calls)
	}
}

func TestQuoteIsPendingWhenTheProviderFails(t *testing.T) {
	svc, _ := newEnergyService(t, &fakeEstimator{err: ErrEnergyUnavailable})
	q, err := svc.Quote(context.Background(), "d-1", slot)
	if err != nil || q.Price.Energy.Status != domain.EnergyPending || q.Price.TotalCents != 2_500_000 {
		t.Fatalf("got %+v err=%v", q, err)
	}
}

func TestQuoteOfUnknownDoctor(t *testing.T) {
	est := &fakeEstimator{microKWh: 1}
	svc, _ := newEnergyService(t, est)
	if _, err := svc.Quote(context.Background(), "nope", slot); !errors.Is(err, ErrUnknownDoctor) || len(est.calls) != 0 {
		t.Fatalf("want ErrUnknownDoctor without provider call, got %v calls=%d", err, len(est.calls))
	}
}

// The inline call must fit inside the gateway's 2 s upstream limit (ADR-005).
func TestInlineEstimateUsesTheInlineBudget(t *testing.T) {
	est := &fakeEstimator{microKWh: 1}
	svc, _ := newEnergyService(t, est)
	svc.EnergyBudget = 300 * time.Millisecond
	_, _ = svc.Quote(context.Background(), "d-1", slot)
	if est.deadline <= 0 || est.deadline > 300*time.Millisecond {
		t.Fatalf("want a deadline within 300ms, got %v", est.deadline)
	}
}

func TestPortalBookingPersistsEstimatedEnergyOutsideTheLock(t *testing.T) {
	for name, book := range map[string]func(*Service) (domain.Appointment, error){
		"Book": func(s *Service) (domain.Appointment, error) { return s.Book(context.Background(), "p-1", "d-1", slot) },
		"BookForPatient": func(s *Service) (domain.Appointment, error) {
			return s.BookForPatient(context.Background(), "p-1", "d-1", slot)
		},
	} {
		est := &fakeEstimator{microKWh: 381_000}
		svc, store := newEnergyService(t, est)
		a, err := book(svc)
		if err != nil {
			t.Fatal(err)
		}
		if a.Energy.Status != domain.EnergyEstimated || a.Energy.CostCents != 6858 || store.rows[a.ID].Energy != a.Energy {
			t.Fatalf("%s: returned %+v stored %+v", name, a.Energy, store.rows[a.ID].Energy)
		}
		if len(est.calls) != 1 {
			t.Fatalf("%s: want one provider call, got %d", name, len(est.calls))
		}
	}
}

func TestBookingSucceedsWithPendingEnergyWhenTheProviderFails(t *testing.T) {
	svc, store := newEnergyService(t, &fakeEstimator{err: ErrEnergyUnavailable})
	a, err := svc.BookForPatient(context.Background(), "p-1", "d-1", slot)
	if err != nil || a.Energy.Status != domain.EnergyPending || store.rows[a.ID].Energy.Status != domain.EnergyPending || len(store.outbox) != 1 {
		t.Fatalf("booking must succeed with pending energy: %+v err=%v", a, err)
	}
	if p := svc.PriceOf(a); p.TotalCents != 2_500_000 {
		t.Fatalf("pending total must be the base price, got %+v", p)
	}
}

func TestBookingWithoutEstimatorIsPending(t *testing.T) {
	svc, _ := newService()
	a, err := svc.Book(context.Background(), "p-1", "d-1", slot)
	if err != nil || a.Energy.Status != domain.EnergyPending {
		t.Fatalf("got %+v err=%v", a, err)
	}
}

func TestRejectedBookingDoesNotCallTheProvider(t *testing.T) {
	est := &fakeEstimator{microKWh: 1}
	svc, _ := newEnergyService(t, est)
	if _, err := svc.Book(context.Background(), "p-1", "d-1", fixedNow.Add(time.Hour)); !errors.Is(err, domain.ErrLeadTime) || len(est.calls) != 0 {
		t.Fatalf("got err=%v calls=%d", err, len(est.calls))
	}
}

// Partner bookings keep their flat ARS 25.000 price (ADR-008): no provider call.
func TestPartnerBookingIsNotPricedByEnergy(t *testing.T) {
	est := &fakeEstimator{microKWh: 1}
	svc, store := newEnergyService(t, est)
	a, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "p-1", "d-1", slot)
	if err != nil || len(est.calls) != 0 || store.rows[a.ID].Energy.Status != domain.EnergyNotApplicable {
		t.Fatalf("got %+v err=%v calls=%d", store.rows[a.ID], err, len(est.calls))
	}
}

func TestReestimatorUpdatesPendingAppointments(t *testing.T) {
	est := &fakeEstimator{err: ErrEnergyUnavailable}
	svc, store := newEnergyService(t, est)
	var ids []string
	for i := 0; i < 3; i++ {
		a, err := svc.Book(context.Background(), fmt.Sprintf("p-%d", i), "d-1", slot.Add(time.Duration(i)*30*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	est.err, est.microKWh = nil, 381_000
	if n := svc.ReestimatePending(context.Background(), 2); n != 2 {
		t.Fatalf("batch of 2: updated %d", n)
	}
	if n := svc.ReestimatePending(context.Background(), 2); n != 1 {
		t.Fatalf("second batch: updated %d", n)
	}
	for _, id := range ids {
		if e := store.rows[id].Energy; e.Status != domain.EnergyEstimated || e.CostCents != 6858 {
			t.Fatalf("%s: %+v", id, e)
		}
	}
}

// A dead provider stops the batch after the first failure instead of
// spending one budget per pending appointment.
func TestReestimatorStopsTheBatchWhenTheProviderFails(t *testing.T) {
	est := &fakeEstimator{err: ErrEnergyUnavailable}
	svc, _ := newEnergyService(t, est)
	for i := 0; i < 3; i++ {
		_, _ = svc.Book(context.Background(), fmt.Sprintf("p-%d", i), "d-1", slot.Add(time.Duration(i)*30*time.Minute))
	}
	est.calls = nil
	if n := svc.ReestimatePending(context.Background(), 10); n != 0 || len(est.calls) != 1 {
		t.Fatalf("updated=%d calls=%d", n, len(est.calls))
	}
}

func TestConfirmedTariffSurvivesConfigurationChange(t *testing.T) {
	est := &fakeEstimator{err: ErrEnergyUnavailable}
	svc, store := newEnergyService(t, est)
	a, err := svc.BookForPatient(context.Background(), "p-1", "d-1", slot)
	if err != nil {
		t.Fatal(err)
	}
	svc.Tariff = domain.Tariff{BaseCents: 3_000_000, CentsPerKWh: 99_000}
	est.err = nil
	est.microKWh = 381_000
	if n := svc.ReestimatePending(context.Background(), 1); n != 1 {
		t.Fatalf("reestimated %d", n)
	}
	persisted, _ := store.Get(context.Background(), a.ID)
	p := svc.PriceOf(persisted)
	if p.BaseCents != 2_500_000 || p.Energy.CostCents != 6858 || p.TotalCents != 2_506_858 {
		t.Fatalf("confirmed price changed: %+v", p)
	}
}

func TestPendingEnergyUsesBookedDurationAfterScheduleChanges(t *testing.T) {
	est := &fakeEstimator{err: ErrEnergyUnavailable}
	svc, store := newEnergyService(t, est)
	ctx := context.Background()
	a, err := svc.Book(ctx, "p-1", "d-1", slot)
	if err != nil {
		t.Fatal(err)
	}
	// The persisted appointment may have a different duration than the initial directory default.
	a.EndAt = a.StartAt.Add(45 * time.Minute)
	store.rows[a.ID] = a
	est.err = nil
	est.microKWh = 1
	if n := svc.ReestimatePending(ctx, 10); n != 1 {
		t.Fatalf("reestimated %d", n)
	}
	if got := est.calls[len(est.calls)-1].Hours; got != 0.75 {
		t.Fatalf("estimated using current/default duration: %v", got)
	}
}
