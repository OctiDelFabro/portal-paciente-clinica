// Package httpapi es el adaptador de entrada HTTP de Turnos.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Metricas de negocio: reason tiene valores acotados (ver app.RejectReason).
var (
	booked = promauto.NewCounter(prometheus.CounterOpts{
		Name: "appointments_booked_total",
		Help: "Turnos reservados correctamente.",
	})
	rejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "appointments_rejected_total",
		Help: "Reservas rechazadas, por motivo de negocio.",
	}, []string{"reason"})
	cancelled = promauto.NewCounter(prometheus.CounterOpts{
		Name: "appointments_cancelled_total",
		Help: "Turnos cancelados por paciente o recepcion.",
	})
)

type UseCases interface {
	Book(ctx context.Context, patientID, doctorID string, startAt time.Time) (domain.Appointment, error)
	Cancel(ctx context.Context, id, reason string) (domain.Appointment, error)
	Get(ctx context.Context, id string) (domain.Appointment, error)
}

type appointmentDTO struct {
	ID           string `json:"id"`
	PatientID    string `json:"patientId"`
	DoctorID     string `json:"doctorId"`
	StartAt      string `json:"startAt"`
	EndAt        string `json:"endAt"`
	Status       string `json:"status"`
	Version      int    `json:"version"`
	CancelReason string `json:"cancelReason,omitempty"`
}

func toDTO(a domain.Appointment) appointmentDTO {
	return appointmentDTO{a.ID, a.PatientID, a.DoctorID, a.StartAt.Format(time.RFC3339), a.EndAt.Format(time.RFC3339), string(a.Status), a.Version, a.CancelReason}
}

func Register(mux *http.ServeMux, uc UseCases) {
	mux.HandleFunc("POST /appointments", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PatientID string `json:"patientId"`
			DoctorID  string `json:"doctorId"`
			StartAt   string `json:"startAt"`
		}
		if !decode(w, r, &in) {
			return
		}
		start, err := time.Parse(time.RFC3339, in.StartAt)
		if err != nil || in.PatientID == "" || in.DoctorID == "" {
			sendError(w, 400, "INVALID_REQUEST", "patientId, doctorId y startAt (RFC3339) son obligatorios.")
			return
		}
		a, err := uc.Book(r.Context(), in.PatientID, in.DoctorID, start)
		if err != nil {
			rejected.WithLabelValues(app.RejectReason(err)).Inc()
			mapError(w, err)
			return
		}
		booked.Inc()
		w.Header().Set("Location", "/appointments/"+a.ID)
		sendJSON(w, 201, toDTO(a))
	})
	mux.HandleFunc("GET /appointments/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, err := uc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			mapError(w, err)
			return
		}
		sendJSON(w, 200, toDTO(a))
	})
	mux.HandleFunc("POST /appointments/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength != 0 && !decode(w, r, &in) {
			return
		}
		if len(in.Reason) > 32 {
			sendError(w, 400, "INVALID_REQUEST", "reason admite hasta 32 caracteres.")
			return
		}
		a, err := uc.Cancel(r.Context(), r.PathValue("id"), in.Reason)
		if err != nil {
			mapError(w, err)
			return
		}
		cancelled.Inc()
		sendJSON(w, 200, toDTO(a))
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		sendError(w, 400, "INVALID_JSON", "JSON invalido o con campos desconocidos.")
		return false
	}
	return true
}

func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrLeadTime):
		sendError(w, 422, "LEAD_TIME_TOO_SHORT", "Se requieren al menos 24 horas de anticipacion.")
	case errors.Is(err, domain.ErrOverlap):
		sendError(w, 409, "SLOT_UNAVAILABLE", "El horario se superpone con otro turno del medico o del paciente.")
	case errors.Is(err, app.ErrIdempotencyConflict):
		sendError(w, 409, "IDEMPOTENCY_CONFLICT", "La Idempotency-Key ya identifica una reserva con otros datos.")
	case errors.Is(err, domain.ErrAlreadyCancelled):
		sendError(w, 409, "ALREADY_CANCELLED", "El turno ya estaba cancelado.")
	case errors.Is(err, domain.ErrPatientNotActive):
		sendError(w, 403, "PATIENT_NOT_ENABLED", "El paciente aun no fue habilitado por recepcion.")
	case errors.Is(err, app.ErrUnknownDoctor):
		sendError(w, 404, "UNKNOWN_DOCTOR", "Medico inexistente.")
	case errors.Is(err, app.ErrUnknownPatient):
		sendError(w, 404, "UNKNOWN_PATIENT", "Paciente inexistente.")
	case errors.Is(err, app.ErrNotFound):
		sendError(w, 404, "NOT_FOUND", "Turno inexistente.")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		// Resultado indeterminado (ADR-005): no se afirma que la operacion fallo.
		sendError(w, 503, "UNAVAILABLE", "No se pudo confirmar la operacion; consulte el estado del turno.")
	default:
		sendError(w, 500, "INTERNAL", "Error interno.")
	}
}

func sendJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func sendError(w http.ResponseWriter, status int, code, msg string) {
	sendJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
