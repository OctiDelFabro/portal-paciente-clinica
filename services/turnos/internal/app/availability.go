package app

import (
	"context"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Availability is the list of free slots of one doctor on one clinic day.
type Availability struct {
	DoctorID string
	Date     string // YYYY-MM-DD in domain.ClinicZone
	Duration time.Duration
	Slots    []domain.Slot
}

// AvailabilityQuery is the read path for availability. Service implements it
// against the authoritative store; the Redis adapter decorates it (ADR-007).
type AvailabilityQuery interface {
	Availability(ctx context.Context, doctorID string, day time.Time) (Availability, error)
}

// AvailabilityCache drops the cached availability of a doctor's day. It runs
// after a commit and never fails the caller: a lost invalidation is bounded by
// the cache TTL, and bookings are always validated under the MySQL lock.
type AvailabilityCache interface {
	Invalidate(ctx context.Context, doctorID, day string)
}

// Availability computes the free slots of doctorID on day (local midnight)
// from the doctor's schedule and the active appointments of that day.
func (s *Service) Availability(ctx context.Context, doctorID string, day time.Time) (Availability, error) {
	doctor, err := s.doctor(ctx, doctorID)
	if err != nil {
		return Availability{}, err
	}
	day = day.In(domain.ClinicZone)
	existing, err := s.Store.ActiveForDoctor(ctx, doctorID, day, day.AddDate(0, 0, 1))
	if err != nil {
		return Availability{}, err
	}
	blocks, err := s.blocks(ctx, doctorID, day, day.AddDate(0, 0, 1))
	if err != nil {
		return Availability{}, err
	}
	slots, err := domain.FreeSlots(doctor, day, s.Now(), domain.WithBlocks(existing, blocks))
	if err != nil {
		return Availability{}, err
	}
	return Availability{DoctorID: doctorID, Date: domain.DayOf(day), Duration: doctor.Duration, Slots: slots}, nil
}

// invalidate drops the cached days an appointment occupies (start and end day).
func (s *Service) invalidate(ctx context.Context, a domain.Appointment) {
	if s.Cache == nil {
		return
	}
	first := domain.DayOf(a.StartAt)
	s.Cache.Invalidate(ctx, a.DoctorID, first)
	if last := domain.DayOf(a.EndAt.Add(-time.Nanosecond)); last != first {
		s.Cache.Invalidate(ctx, a.DoctorID, last)
	}
}
