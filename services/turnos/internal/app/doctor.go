package app

import (
	"context"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Portal use cases for a doctor authenticated by the gateway (X-User-Role: doctor,
// X-Patient-Id carrying the doctor subject ID from a validated JWT).

// ListForDoctor returns the doctor's assigned appointments, most recent first.
func (s *Service) ListForDoctor(ctx context.Context, doctorID string) ([]domain.Appointment, error) {
	return s.Store.ListByDoctor(ctx, doctorID)
}

// GetForDoctor returns ErrNotFound for appointments of other doctors, without
// revealing whether the appointment exists.
func (s *Service) GetForDoctor(ctx context.Context, doctorID, id string) (domain.Appointment, error) {
	a, err := s.Store.Get(ctx, id)
	if err != nil {
		return domain.Appointment{}, err
	}
	if a.DoctorID != doctorID {
		return domain.Appointment{}, ErrNotFound
	}
	return a, nil
}

// CancelForDoctor cancels an appointment belonging to the doctor.
func (s *Service) CancelForDoctor(ctx context.Context, doctorID, id, reason string) (domain.Appointment, error) {
	if _, err := s.GetForDoctor(ctx, doctorID, id); err != nil {
		return domain.Appointment{}, err
	}
	return s.Cancel(ctx, id, reason)
}

func (s *Service) HasRelationship(ctx context.Context, doctor, patient string) (bool, error) {
	return s.Store.HasRelationship(ctx, doctor, patient)
}
