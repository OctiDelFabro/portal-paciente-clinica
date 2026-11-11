package httpapi

import (
	"context"
	"net/http"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"strconv"
	"strings"
	"time"
)

type portalCases interface {
	PortalBook(context.Context, string, string, string, string, time.Time) (domain.Appointment, error)
	PortalGet(context.Context, string, string, string) (domain.Appointment, error)
	PortalCancel(context.Context, string, string, string, string) (domain.Appointment, error)
	PortalPage(context.Context, string, string, string, string, int) ([]domain.Appointment, bool, error)
	Reschedule(context.Context, string, string, string, time.Time) (domain.Appointment, error)
}
type agendaCases interface {
	AuthorizeAgenda(context.Context, string, string, string, bool) error
	Schedule(context.Context, string) (domain.Agenda, error)
	SetSchedule(context.Context, string, domain.Agenda) error
	ListBlocks(context.Context, string) ([]domain.Block, error)
	BlockAgenda(context.Context, string, time.Time, time.Time, string) (domain.Block, error)
	ReleaseBlock(context.Context, string, string) error
}

func registerPortal(mux *http.ServeMux, uc UseCases) {
	p, ok := uc.(portalCases)
	if !ok {
		return
	}
	mux.HandleFunc("POST /appointments/{id}/reschedule", func(w http.ResponseWriter, r *http.Request) {
		caller, ok := authPatient(w, r)
		if !ok {
			return
		}
		if caller == "" {
			sendError(w, 401, "UNAUTHORIZED", "Se requiere autenticación.")
			return
		}
		var in struct {
			StartAt string `json:"startAt"`
		}
		if !decode(w, r, &in) {
			return
		}
		start, e := time.Parse(time.RFC3339, in.StartAt)
		if e != nil {
			sendError(w, 400, "INVALID_REQUEST", "startAt debe ser RFC3339.")
			return
		}
		a, e := p.Reschedule(r.Context(), r.Header.Get("X-User-Role"), caller, r.PathValue("id"), start)
		if e != nil {
			mapError(w, e)
			return
		}
		sendJSON(w, 201, toPortalDTO(uc, a))
	})
	a, ok := uc.(agendaCases)
	if !ok {
		return
	}
	allowed := func(w http.ResponseWriter, r *http.Request, write bool) bool {
		caller, ok := authPatient(w, r)
		if !ok {
			return false
		}
		if caller == "" {
			sendError(w, 401, "UNAUTHORIZED", "Se requiere autenticación.")
			return false
		}
		if err := a.AuthorizeAgenda(r.Context(), r.Header.Get("X-User-Role"), caller, r.PathValue("id"), write); err != nil {
			mapError(w, err)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /doctors/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r, false) {
			return
		}
		v, e := a.Schedule(r.Context(), r.PathValue("id"))
		if e != nil {
			mapError(w, e)
			return
		}
		sendJSON(w, 200, toSchedule(v))
	})
	mux.HandleFunc("PUT /doctors/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r, true) {
			return
		}
		var in scheduleDTO
		if !decode(w, r, &in) {
			return
		}
		v, e := in.agenda(r.PathValue("id"))
		if e == nil {
			e = a.SetSchedule(r.Context(), r.PathValue("id"), v)
		}
		if e != nil {
			mapError(w, e)
			return
		}
		sendJSON(w, 200, toSchedule(v))
	})
	mux.HandleFunc("GET /doctors/{id}/blocks", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r, false) {
			return
		}
		v, e := a.ListBlocks(r.Context(), r.PathValue("id"))
		if e != nil {
			mapError(w, e)
			return
		}
		if v == nil {
			v = []domain.Block{}
		}
		sendJSON(w, 200, map[string]any{"blocks": v})
	})
	mux.HandleFunc("POST /doctors/{id}/blocks", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r, true) {
			return
		}
		var in struct {
			StartAt string `json:"startAt"`
			EndAt   string `json:"endAt"`
			Reason  string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		start, e := time.Parse(time.RFC3339, in.StartAt)
		end, e2 := time.Parse(time.RFC3339, in.EndAt)
		if e != nil || e2 != nil {
			sendError(w, 400, "INVALID_REQUEST", "startAt y endAt deben ser RFC3339.")
			return
		}
		v, e := a.BlockAgenda(r.Context(), r.PathValue("id"), start, end, in.Reason)
		if e != nil {
			mapError(w, e)
			return
		}
		sendJSON(w, 201, v)
	})
	mux.HandleFunc("POST /doctors/{id}/blocks/{block}/release", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r, true) {
			return
		}
		if e := a.ReleaseBlock(r.Context(), r.PathValue("id"), r.PathValue("block")); e != nil {
			mapError(w, e)
			return
		}
		sendJSON(w, 200, map[string]bool{"released": true})
	})
}

type scheduleDTO struct {
	DoctorID        string         `json:"doctorId"`
	DurationMinutes int            `json:"durationMinutes"`
	Weekdays        []time.Weekday `json:"weekdays"`
	Open            string         `json:"open"`
	Close           string         `json:"close"`
}

func clock(d time.Duration) string {
	return strconv.Itoa(int(d/time.Hour)/10) + strconv.Itoa(int(d/time.Hour)%10) + ":" + strconv.Itoa(int(d/time.Minute)%60/10) + strconv.Itoa(int(d/time.Minute)%10)
}
func toSchedule(a domain.Agenda) scheduleDTO {
	return scheduleDTO{a.DoctorID, int(a.Duration / time.Minute), a.Schedule.Weekdays, clock(a.Schedule.Open), clock(a.Schedule.Close)}
}
func parseClock(raw string) (time.Duration, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 || len(raw) != 5 {
		return 0, domain.ErrInvalidAgenda
	}
	for _, i := range []int{0, 1, 3, 4} {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, domain.ErrInvalidAgenda
		}
	}
	h, e := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	if e != nil || e2 != nil || h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return 0, domain.ErrInvalidAgenda
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}
func (d scheduleDTO) agenda(id string) (domain.Agenda, error) {
	if d.DurationMinutes < 5 || d.DurationMinutes > 240 {
		return domain.Agenda{}, domain.ErrInvalidAgenda
	}
	open, e := parseClock(d.Open)
	close, e2 := parseClock(d.Close)
	v := domain.Agenda{DoctorID: id, Duration: time.Duration(d.DurationMinutes) * time.Minute, Schedule: domain.Schedule{Weekdays: d.Weekdays, Open: open, Close: close}}
	if e != nil || e2 != nil {
		return v, domain.ErrInvalidAgenda
	}
	return v, v.Validate()
}

var _ portalCases = (*app.Service)(nil)
