package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Consultation energy cost (ADR-009, decision L6). The provider is called
// outside any transaction: a slow provider never holds the booking lock.

// ErrEnergyUnavailable is the only provider failure the application sees; the
// adapter keeps the provider's codes and types to itself (anticorruption layer).
var ErrEnergyUnavailable = errors.New("energy estimate unavailable")

// EnergyEstimate is the provider answer translated to our model.
type EnergyEstimate struct {
	MicroKWh         int64
	AlgorithmVersion string
}

// EnergyEstimator turns a consultation's equipment into kWh.
type EnergyEstimator interface {
	Estimate(ctx context.Context, c domain.Consumption) (EnergyEstimate, error)
}

// DefaultEnergyBudget bounds the provider call inside a portal request. The
// gateway gives Turnos 2 s per request (ADR-005), so the provider's own 5 s
// budget is only used by the background re-estimator.
const DefaultEnergyBudget = 1200 * time.Millisecond

// ReestimateBudget is the provider's recommended budget, used off the request path.
const ReestimateBudget = 5 * time.Second

// Quote is the price shown before confirming a booking.
type Quote struct {
	DoctorID string
	StartAt  time.Time
	Price    domain.Price
}

// Quote prices a consultation of doctorID at startAt. A provider failure is
// not an error: the energy cost is pending and the total is the base price.
func (s *Service) Quote(ctx context.Context, doctorID string, startAt time.Time) (Quote, error) {
	doctor, err := s.doctor(ctx, doctorID)
	if err != nil {
		return Quote{}, err
	}
	existing, err := s.Store.ActiveForDoctor(ctx, doctorID, startAt, startAt.Add(doctor.Duration))
	if err != nil {
		return Quote{}, err
	}
	blocks, err := s.blocks(ctx, doctorID, startAt, startAt.Add(doctor.Duration))
	if err != nil {
		return Quote{}, err
	}
	if _, err := domain.Book("quote", domain.Patient{Enabled: true}, doctor, startAt, s.Now(), domain.WithBlocks(existing, blocks)); err != nil {
		return Quote{}, err
	}
	e := s.estimate(ctx, doctor, s.inlineBudget())
	return Quote{DoctorID: doctorID, StartAt: startAt, Price: s.Tariff.Price(e)}, nil
}

// PriceOf is the price breakdown of an appointment's stored energy.
func (s *Service) PriceOf(a domain.Appointment) domain.Price {
	tariff := s.Tariff
	if a.Tariff != nil {
		tariff = *a.Tariff
	}
	return tariff.Price(a.Energy)
}

func (s *Service) inlineBudget() time.Duration {
	if s.EnergyBudget > 0 {
		return s.EnergyBudget
	}
	return DefaultEnergyBudget
}

// estimate never fails: without an estimator or on error it returns pending.
// The adapter already logged provider.unavailable with the outcome.
func (s *Service) estimate(ctx context.Context, doctor domain.Doctor, budget time.Duration) domain.Energy {
	return s.estimateAt(ctx, doctor, budget, s.Tariff)
}
func (s *Service) estimateAt(ctx context.Context, doctor domain.Doctor, budget time.Duration, tariff domain.Tariff) domain.Energy {
	if s.Energy == nil {
		return domain.Energy{Status: domain.EnergyPending}
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	est, err := s.Energy.Estimate(ctx, domain.ConsultationConsumption(doctor))
	if err != nil {
		return domain.Energy{Status: domain.EnergyPending}
	}
	return tariff.Estimated(est.MicroKWh, est.AlgorithmVersion)
}

// priceBooking runs after the booking committed. If the estimate or its write
// fails the appointment stays pending and the re-estimator completes it.
func (s *Service) priceBooking(ctx context.Context, a domain.Appointment, doctor domain.Doctor) domain.Appointment {
	tariff := s.Tariff
	if a.Tariff != nil {
		tariff = *a.Tariff
	}
	e := s.estimateAt(ctx, doctor, s.inlineBudget(), tariff)
	if e.Status != domain.EnergyEstimated {
		return a
	}
	if err := s.Store.SaveEnergy(ctx, a.ID, e, s.Now().UTC()); err != nil {
		slog.WarnContext(ctx, "energy estimate not saved", "event", "energy.save_failed", "appointment_id", a.ID, "error", err.Error())
		return a
	}
	a.Energy = e
	return a
}

// ReestimatePending prices up to limit pending future appointments and returns
// how many it updated. The first provider failure ends the batch: the next
// tick retries, and the circuit breaker keeps a dead provider cheap.
func (s *Service) ReestimatePending(ctx context.Context, limit int) int {
	if s.Energy == nil {
		return 0
	}
	now := s.Now().UTC()
	pending, err := s.Store.PendingEnergy(ctx, now, limit)
	if err != nil {
		slog.WarnContext(ctx, "pending energy not listed", "event", "energy.reestimate_failed", "error", err.Error())
		return 0
	}
	updated := 0
	for _, a := range pending {
		doctor, err := s.Directory.Doctor(ctx, a.DoctorID)
		if err != nil {
			// Record the attempt so this row rotates behind the others.
			_ = s.Store.SaveEnergy(ctx, a.ID, domain.Energy{Status: domain.EnergyPending}, now)
			continue
		}
		tariff := s.Tariff
		if a.Tariff != nil {
			tariff = *a.Tariff
		}
		e := s.estimateAt(ctx, doctor, ReestimateBudget, tariff)
		if err := s.Store.SaveEnergy(ctx, a.ID, e, now); err != nil {
			slog.WarnContext(ctx, "energy estimate not saved", "event", "energy.save_failed", "appointment_id", a.ID, "error", err.Error())
			break
		}
		if e.Status != domain.EnergyEstimated {
			break
		}
		updated++
	}
	if updated > 0 {
		slog.InfoContext(ctx, "pending energy estimated", "event", "energy.reestimated", "count", updated)
	}
	return updated
}

// RunEnergyReestimator calls ReestimatePending every interval until ctx ends.
func (s *Service) RunEnergyReestimator(ctx context.Context, every time.Duration, batch int) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ReestimatePending(ctx, batch)
		}
	}
}
