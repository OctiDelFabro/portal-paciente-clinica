package app

import (
	"context"
	"errors"
	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"time"
)

var ErrForbidden = errors.New("access denied")
var ErrPatientMismatch = errors.New("patient is not the caller or a verified dependent")

func (s *Service) actor(ctx context.Context, role, caller string) (domain.Patient, error) {
	p, e := s.Directory.Patient(ctx, caller)
	if e != nil {
		return p, e
	}
	if p.Role != "" && p.Role != role {
		return p, ErrForbidden
	}
	return p, nil
}
func (s *Service) authorizePatient(ctx context.Context, role, caller, target string) error {
	actor, e := s.actor(ctx, role, caller)
	if e != nil {
		return e
	}
	if role == "reception" || role == "admin" {
		if !actor.Enabled {
			return domain.ErrPatientNotActive
		}
		return nil
	}
	if role != "patient" {
		return ErrForbidden
	}
	if target == caller {
		return nil
	}
	child, e := s.Directory.Patient(ctx, target)
	if e != nil {
		return e
	}
	now := s.Now().In(domain.ClinicZone)
	if !actor.Enabled || !child.Enabled || actor.BirthDate.IsZero() || actor.BirthDate.AddDate(18, 0, 0).After(now) || child.BirthDate.IsZero() || !child.BirthDate.AddDate(18, 0, 0).After(now) || child.ResponsibleID != caller || (child.Role != "" && child.Role != "patient") {
		return ErrPatientMismatch
	}
	return nil
}
func (s *Service) PortalBook(ctx context.Context, role, caller, target, doctor string, start time.Time) (domain.Appointment, error) {
	if target == "" {
		target = caller
	}
	if e := s.authorizePatient(ctx, role, caller, target); e != nil {
		return domain.Appointment{}, e
	}
	return s.BookForPatient(ctx, target, doctor, start)
}
func (s *Service) PortalGet(ctx context.Context, role, caller, id string) (domain.Appointment, error) {
	a, e := s.Get(ctx, id)
	if e != nil {
		return a, e
	}
	if role == "doctor" {
		actor, e := s.actor(ctx, role, caller)
		if e != nil {
			return a, e
		}
		if !actor.Enabled || a.DoctorID != caller {
			return domain.Appointment{}, ErrNotFound
		}
		return a, nil
	}
	if e := s.authorizePatient(ctx, role, caller, a.PatientID); e != nil {
		if errors.Is(e, ErrForbidden) || errors.Is(e, ErrPatientMismatch) {
			return domain.Appointment{}, ErrNotFound
		}
		return domain.Appointment{}, e
	}
	return a, nil
}
func (s *Service) PortalCancel(ctx context.Context, role, caller, id, reason string) (domain.Appointment, error) {
	if _, e := s.PortalGet(ctx, role, caller, id); e != nil {
		return domain.Appointment{}, e
	}
	return s.Cancel(ctx, id, reason)
}
func (s *Service) PortalPage(ctx context.Context, role, caller, patient, doctor string, page int) ([]domain.Appointment, bool, error) {
	switch role {
	case "patient":
		if patient == "" {
			patient = caller
		}
		if e := s.authorizePatient(ctx, role, caller, patient); e != nil {
			return nil, false, e
		}
	case "doctor":
		actor, e := s.actor(ctx, role, caller)
		if e != nil {
			return nil, false, e
		}
		if !actor.Enabled || (doctor != "" && doctor != caller) {
			return nil, false, ErrForbidden
		}
		doctor = caller
		patient = ""
	case "reception", "admin":
		if e := s.authorizePatient(ctx, role, caller, patient); e != nil {
			return nil, false, e
		}
	default:
		return nil, false, ErrForbidden
	}
	return s.Store.AppointmentsPage(ctx, patient, doctor, page, s.Now())
}

// Reschedule replaces a reservation in ONE transaction. Old cancellation and
// replacement/outbox commit together; any rejection leaves the old turn intact.
func (s *Service) Reschedule(ctx context.Context, role, caller, id string, start time.Time) (domain.Appointment, error) {
	old, e := s.PortalGet(ctx, role, caller, id)
	if e != nil {
		return domain.Appointment{}, e
	}
	if role == "doctor" {
		return domain.Appointment{}, ErrForbidden
	}
	doctor, e := s.doctor(ctx, old.DoctorID)
	if e != nil {
		return domain.Appointment{}, e
	}
	patient, e := s.Directory.Patient(ctx, old.PatientID)
	if e != nil {
		return domain.Appointment{}, e
	}
	var replacement, cancelled domain.Appointment
	e = s.Store.InTx(ctx, func(tx Tx) error {
		if e := tx.LockResources(ctx, DoctorKey(old.DoctorID), PatientKey(old.PatientID)); e != nil {
			return e
		}
		current, e := tx.GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		cancelled, e = current.Cancel("rescheduled", s.Now())
		if e != nil {
			return e
		}
		d, blocks, e := s.agendaInTx(ctx, tx, doctor, start, start.Add(doctor.Duration), nil)
		if e != nil {
			return e
		}
		existing, e := tx.FindActiveOverlapping(ctx, d.ID, patient.ID, start, start.Add(d.Duration))
		if e != nil {
			return e
		}
		filtered := append([]domain.Appointment(nil), blocks...)
		for _, a := range existing {
			if a.ID != id {
				filtered = append(filtered, a)
			}
		}
		replacement, e = domain.Book(s.NewID(), patient, d, start, s.Now(), filtered)
		if e != nil {
			return e
		}
		// A reschedule is a new reservation: the current duration/tariff apply.
		tariff := s.Tariff
		replacement.Tariff = &tariff
		replacement.Energy = domain.Energy{Status: domain.EnergyPending}
		if e := tx.Update(ctx, cancelled); e != nil {
			return e
		}
		if e := tx.Insert(ctx, replacement); e != nil {
			return e
		}
		if e := tx.AppendOutbox(ctx, s.event(ctx, messaging.RoutingCancel, cancelled, eventOptions{Operation: "reschedule"})); e != nil {
			return e
		}
		return tx.AppendOutbox(ctx, s.event(ctx, messaging.RoutingRescheduled, replacement, eventOptions{Previous: &cancelled}))
	})
	if e != nil {
		return domain.Appointment{}, e
	}
	s.invalidate(ctx, cancelled)
	s.invalidate(ctx, replacement)
	doctor.Duration = replacement.EndAt.Sub(replacement.StartAt)
	return s.priceBooking(ctx, replacement, doctor), nil
}
