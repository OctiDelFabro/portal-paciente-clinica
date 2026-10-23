// Package service es la capa de negocio de Notificaciones (arquitectura en
// capas, ADR-002): api -> service -> repository / mailer.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/notifications/internal/repository"
)

// PortalConsumer identifica las notificaciones generadas por eventos internos.
const PortalConsumer = "portal-internal"

var (
	ErrUnprocessable = errors.New("unprocessable event")
	ErrNoContact     = errors.New("patient has no contact address")
)

var (
	replays = promauto.NewCounter(prometheus.CounterOpts{
		Name: "idempotency_replays_total",
		Help: "POST repetidos con la misma clave y datos: reintentos correctos del consumidor.",
	})
	conflicts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "idempotency_conflicts_total",
		Help: "POST con clave reutilizada y datos distintos: integracion incorrecta del consumidor.",
	})
)

type Recipient struct {
	Email string `json:"email"`
}

type Request struct {
	TemplateID string            `json:"templateId"`
	Recipient  Recipient         `json:"recipient"`
	Parameters map[string]string `json:"parameters"`
}

// Contacts obtiene destinatarios desde Pacientes. Para un menor devuelve su
// correo y el del responsable; sin correo propio, solo el del responsable (RF-12).
type Contacts interface {
	Emails(ctx context.Context, patientID string) ([]string, error)
}

type Service struct {
	Repo     *repository.Repo
	Contacts Contacts
	Mailer   Mailer
	Breaker  *Breaker
	Now      func() time.Time
	NewID    func() string
	TTL      time.Duration
}

// Fingerprint ignora el orden de las propiedades: json.Marshal ordena las claves de los mapas.
func Fingerprint(r Request) string {
	canonical, _ := json.Marshal(r)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func traceHeaders(ctx context.Context) map[string]string {
	h := map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(h))
	if id := obs.CorrelationID(ctx); id != "" {
		h["x-correlation-id"] = id
	}
	return h
}

// Accept registra una solicitud externa de forma durable antes del 202.
func (s *Service) Accept(ctx context.Context, consumer, key string, r Request) (repository.Notification, bool, error) {
	now := s.Now().UTC()
	n := repository.Notification{ID: s.NewID(), Consumer: consumer, Source: "api", TemplateID: r.TemplateID,
		Recipient: r.Recipient.Email, Params: r.Parameters, Status: "accepted", TraceHeaders: traceHeaders(ctx),
		CreatedAt: now, UpdatedAt: now}
	got, replayed, err := s.Repo.CreateIdempotent(ctx, key, Fingerprint(r), n, s.TTL)
	switch {
	case errors.Is(err, repository.ErrIdempotencyConflict):
		conflicts.Inc()
	case err == nil && replayed:
		replays.Inc()
	case err == nil:
		slog.InfoContext(ctx, "notification accepted", "event", "notification.accepted", "notification_id", got.ID, "consumer", consumer, "template_id", r.TemplateID)
	}
	return got, replayed, err
}

func (s *Service) Get(ctx context.Context, id, consumer string) (repository.Notification, error) {
	return s.Repo.Get(ctx, id, consumer)
}

// Event es el sobre de docs/contracts/turnos-events-v1.schema.json.
type Event struct {
	EventID       string `json:"eventId"`
	EventType     string `json:"eventType"`
	Version       int    `json:"version"`
	OccurredAt    string `json:"occurredAt"`
	CorrelationID string `json:"correlationId"`
	Data          struct {
		TurnID             string `json:"turnId"`
		PatientID          string `json:"patientId"`
		DoctorID           string `json:"doctorId"`
		StartAt            string `json:"startAt"`
		EndAt              string `json:"endAt"`
		AppointmentVersion int    `json:"appointmentVersion"`
		ReasonCode         string `json:"reasonCode"`
	} `json:"data"`
}

var eventTemplates = map[string]struct{ template, notice string }{
	"turnos.reservado.v1": {"operation-confirmed.v1", "appointment_booked"},
	"turnos.cancelado.v1": {"operation-cancelled.v1", "appointment_cancelled"},
}

// ParseEvent valida el sobre. Un evento invalido no mejora con reintentos:
// se informa como ErrUnprocessable para enviarlo directo a la DLQ.
func ParseEvent(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return e, errors.Join(ErrUnprocessable, err)
	}
	_, known := eventTemplates[e.EventType]
	_, startErr := time.Parse(time.RFC3339, e.Data.StartAt)
	if !known || e.Version != 1 || !obs.ValidCorrelationID(e.EventID) || e.Data.TurnID == "" || e.Data.PatientID == "" || startErr != nil {
		return e, ErrUnprocessable
	}
	if e.EventType == "turnos.cancelado.v1" && e.Data.ReasonCode == "" {
		return e, ErrUnprocessable
	}
	return e, nil
}

// HandleEvent crea el aviso del portal y un correo por destinatario.
// duplicate=true indica que el evento ya habia sido procesado.
func (s *Service) HandleEvent(ctx context.Context, e Event) (bool, error) {
	mapping := eventTemplates[e.EventType]
	emails, err := s.Contacts.Emails(ctx, e.Data.PatientID)
	if errors.Is(err, ErrNoContact) {
		// Sin destinatario no hay correo, pero el aviso del portal se publica igual.
		emails, err = nil, nil
	}
	if err != nil {
		return false, err
	}
	now := s.Now().UTC()
	params := map[string]string{"operationName": "Turno medico", "reference": e.Data.TurnID}
	if mapping.template == "operation-confirmed.v1" {
		params["scheduledAt"] = e.Data.StartAt
	} else {
		params["reason"] = e.Data.ReasonCode
	}
	headers := traceHeaders(ctx)
	var ns []repository.Notification
	for _, email := range emails {
		ns = append(ns, repository.Notification{ID: s.NewID(), Consumer: PortalConsumer, Source: "event", TemplateID: mapping.template,
			Recipient: email, Params: params, Status: "accepted", TraceHeaders: headers, CreatedAt: now, UpdatedAt: now})
	}
	notice := repository.Notice{ID: s.NewID(), PatientID: e.Data.PatientID, TurnID: e.Data.TurnID, Kind: mapping.notice}
	return s.Repo.RecordEvent(ctx, e.EventID, e.EventType, now, ns, notice)
}
