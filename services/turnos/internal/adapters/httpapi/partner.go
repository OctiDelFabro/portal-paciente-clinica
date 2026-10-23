package httpapi

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/internal/platform/apikey"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// partnerRequests: consumer toma solo los nombres configurados (o "unknown");
// operation y outcome tienen valores fijos (ADR-011).
var partnerRequests = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "turnos_partner_requests_total",
	Help: "Solicitudes a la API publicada de Turnos, por consumidor, operacion y resultado.",
}, []string{"consumer", "operation", "outcome"})

var (
	safeKey     = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	periodShape = regexp.MustCompile(`^\d{4}-\d{2}$`)
)

type PartnerUseCases interface {
	BookForPartner(ctx context.Context, consumer, key, patientID, doctorID string, startAt time.Time) (domain.Appointment, bool, error)
	GetForPartner(ctx context.Context, consumer, id string) (domain.Appointment, error)
	CancelForPartner(ctx context.Context, consumer, id string) (domain.Appointment, error)
	Usage(ctx context.Context, consumer string, month time.Time) (app.Usage, error)
	Doctors(ctx context.Context) ([]domain.Doctor, error)
}

// Partner expone la capacidad publicada de Turnos (contrato turnos-partner-v1)
// a consumidores externos autenticados por X-API-Key.
type Partner struct {
	UseCases PartnerUseCases
	Keys     map[string]string // consumidor -> clave, desde configuracion
	Limiter  *apikey.Limiter
	Price    app.Pricing
	Now      func() time.Time
}

type partnerHandler func(w http.ResponseWriter, r *http.Request, consumer string) (outcome string)

func (p *Partner) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/appointments", p.auth("book", p.book))
	mux.HandleFunc("GET /v1/appointments/{id}", p.auth("get", func(w http.ResponseWriter, r *http.Request, consumer string) string {
		a, err := p.UseCases.GetForPartner(r.Context(), consumer, r.PathValue("id"))
		return p.reply(w, 200, a, err)
	}))
	mux.HandleFunc("POST /v1/appointments/{id}/cancel", p.auth("cancel", func(w http.ResponseWriter, r *http.Request, consumer string) string {
		a, err := p.UseCases.CancelForPartner(r.Context(), consumer, r.PathValue("id"))
		return p.reply(w, 200, a, err)
	}))
	mux.HandleFunc("GET /v1/usage", p.auth("usage", p.usage))
	mux.HandleFunc("GET /v1/doctors", p.auth("doctors", func(w http.ResponseWriter, r *http.Request, consumer string) string {
		ds, err := p.UseCases.Doctors(r.Context())
		if err != nil {
			mapError(w, err)
			return app.RejectReason(err)
		}
		type doctorDTO struct {
			ID              string `json:"id"`
			DurationMinutes int    `json:"durationMinutes"`
		}
		out := make([]doctorDTO, 0, len(ds))
		for _, d := range ds {
			out = append(out, doctorDTO{d.ID, int(d.Duration / time.Minute)})
		}
		sendJSON(w, 200, map[string]any{"doctors": out})
		return "ok"
	}))
	for _, path := range []string{"/v1/appointments", "/v1/appointments/{id}", "/v1/appointments/{id}/cancel", "/v1/usage", "/v1/doctors"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			sendError(w, 405, "METHOD_NOT_ALLOWED", "Metodo no permitido.")
		})
	}
}

func (p *Partner) auth(operation string, next partnerHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Las respuestas son datos de un consumidor: ningun intermediario debe guardarlas.
		w.Header().Set("Cache-Control", "no-store")
		consumer := apikey.Owner(p.Keys, r.Header.Get("X-API-Key"))
		if consumer == "" {
			partnerRequests.WithLabelValues("unknown", operation, "unauthorized").Inc()
			sendError(w, 401, "UNAUTHORIZED", "Clave de API ausente o invalida.")
			return
		}
		if ok, wait := p.Limiter.Allow(consumer, p.Now()); !ok {
			partnerRequests.WithLabelValues(consumer, operation, "rate_limited").Inc()
			w.Header().Set("Retry-After", fmt.Sprint(wait))
			sendError(w, 429, "RATE_LIMITED", "Limite de solicitudes excedido.")
			return
		}
		partnerRequests.WithLabelValues(consumer, operation, next(w, r, consumer)).Inc()
	}
}

func (p *Partner) reply(w http.ResponseWriter, status int, a domain.Appointment, err error) string {
	if err != nil {
		mapError(w, err)
		return app.RejectReason(err)
	}
	sendJSON(w, status, toDTO(a))
	return "ok"
}

func (p *Partner) book(w http.ResponseWriter, r *http.Request, consumer string) string {
	key := r.Header.Get("Idempotency-Key")
	if !safeKey.MatchString(key) {
		sendError(w, 400, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key requiere de 1 a 128 caracteres alfanumericos o ._:-.")
		return "invalid"
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		sendError(w, 415, "UNSUPPORTED_MEDIA_TYPE", "Content-Type debe ser application/json.")
		return "invalid"
	}
	var in struct {
		PatientID string `json:"patientId"`
		DoctorID  string `json:"doctorId"`
		StartAt   string `json:"startAt"`
	}
	if !decode(w, r, &in) {
		return "invalid"
	}
	start, err := time.Parse(time.RFC3339, in.StartAt)
	if err != nil || in.PatientID == "" || in.DoctorID == "" {
		sendError(w, 400, "INVALID_REQUEST", "patientId, doctorId y startAt (RFC3339) son obligatorios.")
		return "invalid"
	}
	a, replayed, err := p.UseCases.BookForPartner(r.Context(), consumer, key, in.PatientID, in.DoctorID, start)
	if err != nil {
		mapError(w, err)
		return app.RejectReason(err)
	}
	// Una repeticion devuelve el mismo 201: el consumidor no necesita distinguirla
	// para registrar el turno, pero puede verla en Idempotency-Replayed.
	w.Header().Set("Idempotency-Replayed", fmt.Sprint(replayed))
	w.Header().Set("Location", "/v1/appointments/"+a.ID)
	sendJSON(w, 201, toDTO(a))
	if replayed {
		return "replayed"
	}
	return "ok"
}

func (p *Partner) usage(w http.ResponseWriter, r *http.Request, consumer string) string {
	month := p.Now().UTC()
	if raw := r.URL.Query().Get("period"); raw != "" {
		parsed, err := time.Parse("2006-01", raw)
		if !periodShape.MatchString(raw) || err != nil {
			sendError(w, 400, "INVALID_PERIOD", "period debe tener el formato AAAA-MM.")
			return "invalid"
		}
		month = parsed
	}
	u, err := p.UseCases.Usage(r.Context(), consumer, month)
	if err != nil {
		mapError(w, err)
		return app.RejectReason(err)
	}
	type itemDTO struct {
		AppointmentID string `json:"appointmentId"`
		BookedAt      string `json:"bookedAt"`
		PriceCents    int64  `json:"priceCents"`
		Currency      string `json:"currency"`
		Status        string `json:"status"`
	}
	type totalDTO struct {
		Currency    string `json:"currency"`
		Bookings    int    `json:"bookings"`
		AmountCents int64  `json:"amountCents"`
	}
	items := make([]itemDTO, 0, len(u.Items))
	for _, it := range u.Items {
		items = append(items, itemDTO{it.AppointmentID, it.BookedAt.UTC().Format(time.RFC3339), it.PriceCents, it.Currency, string(it.Status)})
	}
	totals := make([]totalDTO, 0, len(u.Totals))
	for _, t := range u.Totals {
		totals = append(totals, totalDTO{t.Currency, t.Bookings, t.AmountCents})
	}
	sendJSON(w, 200, map[string]any{
		"consumer":     consumer,
		"period":       u.From.Format("2006-01"),
		"from":         u.From.Format(time.RFC3339),
		"to":           u.To.Format(time.RFC3339),
		"currentPrice": map[string]any{"amountCents": p.Price.Cents, "currency": p.Price.Currency},
		"totals":       totals,
		"items":        items,
	})
	return "ok"
}
