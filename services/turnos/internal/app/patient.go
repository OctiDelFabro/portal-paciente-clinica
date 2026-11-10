package app

import (
	"context"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Portal use cases for a patient authenticated by the gateway (X-Patient-Id
// from a validated JWT). Verification is always read from Pacientes.

// BookForPatient books for an authenticated patient.
func (s *Service) BookForPatient(ctx context.Context, patientID, doctorID string, startAt time.Time) (domain.Appointment, error) {
	a, _, err := s.book(ctx, patientID, doctorID, startAt, nil, true)
	return a, err
}

// ListForPatient returns the patient's appointments, most recent first.
func (s *Service) ListForPatient(ctx context.Context, patientID string) ([]domain.Appointment, error) {
	return s.Store.ListByPatient(ctx, patientID)
}

// GetForPatient returns ErrNotFound for appointments of other patients, without
// revealing whether the appointment exists.
func (s *Service) GetForPatient(ctx context.Context, patientID, id string) (domain.Appointment, error) {
	a, err := s.Store.Get(ctx, id)
	if err != nil {
		return domain.Appointment{}, err
	}
	if a.PatientID != patientID {
		return domain.Appointment{}, ErrNotFound
	}
	return a, nil
}

func (s *Service) CancelForPatient(ctx context.Context, patientID, id, reason string) (domain.Appointment, error) {
	if _, err := s.GetForPatient(ctx, patientID, id); err != nil {
		return domain.Appointment{}, err
	}
	return s.Cancel(ctx, id, reason)
}
