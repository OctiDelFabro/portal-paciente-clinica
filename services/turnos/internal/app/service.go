// Package app orquesta los casos de uso de Turnos. Define los puertos que los
// adaptadores (MySQL, directorio, HTTP) implementan; no conoce sus tecnologias.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrUnknownDoctor  = errors.New("unknown doctor")
	ErrUnknownPatient = errors.New("unknown patient")
)

// Directory obtiene datos de Medicos y Pacientes. En la Entrega 2 lo resuelve
// un adaptador con datos semilla; luego sera un cliente HTTP de esos servicios.
type Directory interface {
	Doctor(ctx context.Context, id string) (domain.Doctor, error)
	Patient(ctx context.Context, id string) (domain.Patient, error)
	Doctors(ctx context.Context) ([]domain.Doctor, error)
}

// OutboxMessage es un evento pendiente de publicar, guardado en la misma
// transaccion que el cambio de negocio (ADR-005).
type OutboxMessage struct {
	EventID    string
	RoutingKey string
	Payload    []byte
	Headers    map[string]string
}

// Tx representa una transaccion del almacenamiento autoritativo.
type Tx interface {
	// LockResources bloquea las filas de recurso en orden estable (ADR-003).
	LockResources(ctx context.Context, keys ...string) error
	FindActiveOverlapping(ctx context.Context, doctorID, patientID string, start, end time.Time) ([]domain.Appointment, error)
	GetForUpdate(ctx context.Context, id string) (domain.Appointment, error)
	Insert(ctx context.Context, a domain.Appointment) error
	Update(ctx context.Context, a domain.Appointment) error
	AppendOutbox(ctx context.Context, m OutboxMessage) error
	FindPartnerBooking(ctx context.Context, consumer, key string) (PartnerBooking, error)
	InsertPartnerBooking(ctx context.Context, pb PartnerBooking) error
}

type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
	Get(ctx context.Context, id string) (domain.Appointment, error)
	PartnerBookingByKey(ctx context.Context, consumer, key string) (PartnerBooking, error)
	PartnerBookingOf(ctx context.Context, appointmentID string) (PartnerBooking, error)
	PartnerUsage(ctx context.Context, consumer string, from, to time.Time) ([]UsageItem, error)
}

type Service struct {
	Store     Store
	Directory Directory
	Now       func() time.Time
	NewID     func() string
	Price     Pricing
}

func DoctorKey(id string) string  { return "doctor:" + id }
func PatientKey(id string) string { return "patient:" + id }

// Book reserva un turno: valida actores, bloquea medico y paciente, comprueba
// superposiciones y registra turno y evento en una unica transaccion.
func (s *Service) Book(ctx context.Context, patientID, doctorID string, startAt time.Time) (domain.Appointment, error) {
	a, _, err := s.book(ctx, patientID, doctorID, startAt, nil)
	return a, err
}

// book es la reserva comun. Con partner, la clave de idempotencia se bloquea y
// se vuelve a comprobar dentro de la transaccion: si otra solicitud con la
// misma clave gano, se devuelve su turno (replayed) en lugar de crear otro.
func (s *Service) book(ctx context.Context, patientID, doctorID string, startAt time.Time, partner *PartnerBooking) (domain.Appointment, bool, error) {
	doctor, err := s.Directory.Doctor(ctx, doctorID)
	if err != nil {
		return domain.Appointment{}, false, err
	}
	patient, err := s.Directory.Patient(ctx, patientID)
	if err != nil {
		return domain.Appointment{}, false, err
	}
	var booked domain.Appointment
	var replayed bool
	err = s.Store.InTx(ctx, func(tx Tx) error {
		replayed = false
		keys := []string{DoctorKey(doctorID), PatientKey(patientID)}
		if partner != nil {
			keys = append(keys, IdempotencyLockKey(partner.Consumer, partner.IdempotencyKey))
		}
		if err := tx.LockResources(ctx, keys...); err != nil {
			return err
		}
		if partner != nil {
			prev, err := tx.FindPartnerBooking(ctx, partner.Consumer, partner.IdempotencyKey)
			switch {
			case err == nil:
				if prev.Fingerprint != partner.Fingerprint {
					return ErrIdempotencyConflict
				}
				booked, err = tx.GetForUpdate(ctx, prev.AppointmentID)
				replayed = err == nil
				return err
			case !errors.Is(err, ErrNotFound):
				return err
			}
		}
		end := startAt.Add(doctor.Duration)
		existing, err := tx.FindActiveOverlapping(ctx, doctorID, patientID, startAt, end)
		if err != nil {
			return err
		}
		booked, err = domain.Book(s.NewID(), patient, doctor, startAt, s.Now(), existing)
		if err != nil {
			return err
		}
		if err := tx.Insert(ctx, booked); err != nil {
			return err
		}
		if partner != nil {
			partner.AppointmentID, partner.BookedAt = booked.ID, s.Now().UTC()
			if err := tx.InsertPartnerBooking(ctx, *partner); err != nil {
				return err
			}
		}
		return tx.AppendOutbox(ctx, s.event(ctx, messaging.RoutingBooked, booked))
	})
	if err != nil {
		slog.WarnContext(ctx, "appointment rejected", "event", "appointment.rejected", "reason", RejectReason(err), "doctor_id", doctorID)
		return domain.Appointment{}, false, err
	}
	if !replayed {
		slog.InfoContext(ctx, "appointment booked", "event", "appointment.booked", "appointment_id", booked.ID, "doctor_id", doctorID)
	}
	return booked, replayed, nil
}

// Cancel cancela un turno respetando la anticipacion minima.
func (s *Service) Cancel(ctx context.Context, id, reason string) (domain.Appointment, error) {
	if reason == "" {
		// El esquema exige reasonCode en turnos.cancelado.v1.
		reason = "patient_request"
	}
	var cancelled domain.Appointment
	err := s.Store.InTx(ctx, func(tx Tx) error {
		current, err := tx.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		cancelled, err = current.Cancel(reason, s.Now())
		if err != nil {
			return err
		}
		if err := tx.Update(ctx, cancelled); err != nil {
			return err
		}
		return tx.AppendOutbox(ctx, s.event(ctx, messaging.RoutingCancel, cancelled))
	})
	if err != nil {
		slog.WarnContext(ctx, "cancellation rejected", "event", "appointment.cancel_rejected", "reason", RejectReason(err), "appointment_id", id)
		return domain.Appointment{}, err
	}
	slog.InfoContext(ctx, "appointment cancelled", "event", "appointment.cancelled", "appointment_id", id)
	return cancelled, nil
}

func (s *Service) Get(ctx context.Context, id string) (domain.Appointment, error) {
	return s.Store.Get(ctx, id)
}

// RejectReason traduce un error a un motivo acotado, usado en logs y como
// etiqueta de metricas (cardinalidad fija).
func RejectReason(err error) string {
	switch {
	case errors.Is(err, domain.ErrLeadTime):
		return "lead_time"
	case errors.Is(err, domain.ErrOverlap):
		return "overlap"
	case errors.Is(err, domain.ErrPatientNotActive):
		return "patient_not_enabled"
	case errors.Is(err, domain.ErrAlreadyCancelled):
		return "already_cancelled"
	case errors.Is(err, ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, ErrUnknownDoctor), errors.Is(err, ErrUnknownPatient), errors.Is(err, ErrNotFound):
		return "not_found"
	default:
		return "internal"
	}
}

// eventEnvelope sigue docs/contracts/turnos-events-v1.schema.json.
type eventEnvelope struct {
	EventID       string    `json:"eventId"`
	EventType     string    `json:"eventType"`
	Version       int       `json:"version"`
	OccurredAt    string    `json:"occurredAt"`
	CorrelationID string    `json:"correlationId"`
	Data          eventData `json:"data"`
}

type eventData struct {
	TurnID             string `json:"turnId"`
	PatientID          string `json:"patientId"`
	DoctorID           string `json:"doctorId"`
	StartAt            string `json:"startAt"`
	EndAt              string `json:"endAt"`
	AppointmentVersion int    `json:"appointmentVersion"`
	ReasonCode         string `json:"reasonCode,omitempty"`
}

func (s *Service) event(ctx context.Context, routingKey string, a domain.Appointment) OutboxMessage {
	correlation := obs.CorrelationID(ctx)
	if correlation == "" {
		correlation = s.NewID()
	}
	env := eventEnvelope{
		EventID: s.NewID(), EventType: routingKey, Version: 1,
		OccurredAt: s.Now().UTC().Format(time.RFC3339Nano), CorrelationID: correlation,
		Data: eventData{TurnID: a.ID, PatientID: a.PatientID, DoctorID: a.DoctorID,
			StartAt: a.StartAt.Format(time.RFC3339), EndAt: a.EndAt.Format(time.RFC3339),
			AppointmentVersion: a.Version, ReasonCode: a.CancelReason},
	}
	payload, _ := json.Marshal(env)
	// Se guarda el contexto de traza para que el publicador asincrono continue la misma traza.
	headers := map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))
	headers["x-correlation-id"] = correlation
	return OutboxMessage{EventID: env.EventID, RoutingKey: routingKey, Payload: payload, Headers: headers}
}
