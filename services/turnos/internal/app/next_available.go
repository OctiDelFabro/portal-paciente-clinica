package app

import (
	"context"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// NextAvailableHorizonDays bounds the search for the first free slot. Beyond it
// the portal shows "no free slots" instead of scanning an unbounded agenda.
const NextAvailableHorizonDays = 60

// NextAvailable returns the first slot of doctorID that Book would accept now:
// inside the doctor's schedule, at least MinLeadTime ahead and without overlap.
// Turnos owns schedules and the agenda, so this is the authoritative value; the
// nextAvailableAt that Medicos indexes is only an approximate ordering key
// (ADR-006). It returns nil when no slot exists within the horizon.
func (s *Service) NextAvailable(ctx context.Context, doctorID string) (*domain.Slot, error) {
	doctor, err := s.Directory.Doctor(ctx, doctorID)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	start := now.Add(domain.MinLeadTime).In(domain.ClinicZone)
	first, err := domain.ParseDay(domain.DayOf(start))
	if err != nil {
		return nil, err
	}
	end := first.AddDate(0, 0, NextAvailableHorizonDays)
	// One read for the whole horizon instead of one per day.
	existing, err := s.Store.ActiveForDoctor(ctx, doctorID, first, end)
	if err != nil {
		return nil, err
	}
	for day := first; day.Before(end); day = day.AddDate(0, 0, 1) {
		slots, err := domain.FreeSlots(doctor, day, now, existing)
		if err != nil {
			return nil, err
		}
		if len(slots) > 0 {
			return &slots[0], nil
		}
	}
	return nil, nil
}
