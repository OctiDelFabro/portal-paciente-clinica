package app

import (
	"context"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

const PageSize = 100

// Page includes active upcoming appointments first. History is still reachable
// by the advertised next page instead of being silently truncated.
func (s *Service) ListAppointments(ctx context.Context, role, caller string, page int) ([]domain.Appointment, bool, error) {
	patient, doctor := caller, ""
	if role == "doctor" {
		patient, doctor = "", caller
	}
	return s.Store.AppointmentsPage(ctx, patient, doctor, page, s.Now())
}
