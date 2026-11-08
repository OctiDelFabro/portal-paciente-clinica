package httpapi

import (
	"context"
	"net/http"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type slotDTO struct {
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
}

type availabilityDTO struct {
	DoctorID        string    `json:"doctorId"`
	Date            string    `json:"date"`
	DurationMinutes int       `json:"durationMinutes"`
	Slots           []slotDTO `json:"slots"`
}

// RegisterAvailability exposes GET /doctors/{id}/availability?date=YYYY-MM-DD.
// q is the Service, or the Redis cache-aside decorator when CACHE_ENABLED=true.
// Slot times are clinic-local RFC3339, ready to send as startAt in a booking.
func RegisterAvailability(mux *http.ServeMux, q app.AvailabilityQuery) {
	mux.HandleFunc("GET /doctors/{id}/availability", func(w http.ResponseWriter, r *http.Request) {
		day, err := domain.ParseDay(r.URL.Query().Get("date"))
		if err != nil {
			sendError(w, 400, "INVALID_DATE", "date es obligatorio con formato AAAA-MM-DD.")
			return
		}
		av, err := q.Availability(r.Context(), r.PathValue("id"), day)
		if err != nil {
			mapError(w, err)
			return
		}
		out := availabilityDTO{DoctorID: av.DoctorID, Date: av.Date, DurationMinutes: int(av.Duration / time.Minute), Slots: make([]slotDTO, 0, len(av.Slots))}
		for _, s := range av.Slots {
			out.Slots = append(out.Slots, slotDTO{s.StartAt.In(domain.ClinicZone).Format(time.RFC3339), s.EndAt.In(domain.ClinicZone).Format(time.RFC3339)})
		}
		sendJSON(w, 200, out)
	})
}

// healthTimeout stays below the gateway health-check timeout (500 ms, ADR-012).
const healthTimeout = 400 * time.Millisecond

// Health answers GET /healthz: 503 when the database does not answer a ping
// within healthTimeout, so the gateway marks the instance down (ADR-012).
func Health(ping func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
		defer cancel()
		status, code := "ok", 200
		if err := ping(ctx); err != nil {
			status, code = "degraded", 503
		}
		sendJSON(w, code, map[string]string{"service": "turnos", "status": status})
	}
}

// WithInstance adds X-Instance so balancing is visible from the client side (ADR-012).
func WithInstance(instance string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Instance", instance)
		next.ServeHTTP(w, r)
	})
}
