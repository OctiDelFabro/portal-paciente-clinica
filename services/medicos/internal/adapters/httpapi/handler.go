// Package httpapi is the HTTP input adapter of Medicos. Search and doctor
// reads are public; profile writes and reindexing require the admin role that
// the gateway forwards from a validated JWT.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

// RoleHeader is set by the gateway from the JWT; client copies are stripped.
const RoleHeader = "X-User-Role"

type UseCases interface {
	UpdateProfile(ctx context.Context, id string, p domain.Profile) (domain.Doctor, error)
	Doctor(ctx context.Context, id string) (domain.Doctor, error)
	Specialties(ctx context.Context) ([]domain.Specialty, error)
	Search(ctx context.Context, q domain.Query) (domain.Page, error)
	Reindex(ctx context.Context) (string, int, error)
}

type doctorDTO struct {
	ID             string   `json:"id"`
	FullName       string   `json:"fullName"`
	Specialties    []string `json:"specialties"`
	AcceptsMinors  bool     `json:"acceptsMinors"`
	Active         bool     `json:"active"`
	ProfileVersion int64    `json:"profileVersion"`
}

type resultDTO struct {
	ID              string   `json:"id"`
	FullName        string   `json:"fullName"`
	Specialties     []string `json:"specialties"`
	AcceptsMinors   bool     `json:"acceptsMinors"`
	NextAvailableAt string   `json:"nextAvailableAt,omitempty"`
}

func toDTO(d domain.Doctor) doctorDTO {
	return doctorDTO{d.ID, d.FullName, d.Specialties, d.AcceptsMinors, d.Active, d.ProfileVersion}
}

func Register(mux *http.ServeMux, uc UseCases) {
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r)
		if err != nil {
			sendError(w, 422, "VALIDATION_ERROR", err.Error())
			return
		}
		page, err := uc.Search(r.Context(), q)
		if err != nil {
			mapError(w, err)
			return
		}
		items := make([]resultDTO, 0, len(page.Items))
		for _, d := range page.Items {
			item := resultDTO{ID: d.DoctorID, FullName: d.FullName, Specialties: d.Specialties, AcceptsMinors: d.AcceptsMinors}
			if d.NextAvailableAt != nil {
				item.NextAvailableAt = d.NextAvailableAt.UTC().Format(time.RFC3339)
			}
			items = append(items, item)
		}
		sendJSON(w, 200, map[string]any{"items": items, "page": q.Page, "size": q.Size, "total": page.Total})
	})
	mux.HandleFunc("GET /doctors/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := uc.Doctor(r.Context(), r.PathValue("id"))
		if err != nil {
			mapError(w, err)
			return
		}
		sendJSON(w, 200, toDTO(d))
	})
	mux.HandleFunc("PUT /doctors/{id}", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			FullName      *string  `json:"fullName"`
			Specialties   []string `json:"specialties"`
			AcceptsMinors *bool    `json:"acceptsMinors"`
			Active        *bool    `json:"active"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.FullName == nil || in.Specialties == nil || in.AcceptsMinors == nil || in.Active == nil {
			sendError(w, 400, "INVALID_REQUEST", "fullName, specialties, acceptsMinors and active are required.")
			return
		}
		d, err := uc.UpdateProfile(r.Context(), r.PathValue("id"), domain.Profile{FullName: strings.TrimSpace(*in.FullName),
			Specialties: in.Specialties, AcceptsMinors: *in.AcceptsMinors, Active: *in.Active})
		if err != nil {
			mapError(w, err)
			return
		}
		slog.InfoContext(r.Context(), "doctor profile updated", "event", "doctor.updated", "doctor_id", d.ID, "profile_version", d.ProfileVersion)
		sendJSON(w, 200, toDTO(d))
	}))
	mux.HandleFunc("GET /specialties", func(w http.ResponseWriter, r *http.Request) {
		list, err := uc.Specialties(r.Context())
		if err != nil {
			mapError(w, err)
			return
		}
		out := make([]map[string]string, 0, len(list))
		for _, s := range list {
			out = append(out, map[string]string{"code": s.Code, "name": s.Name})
		}
		sendJSON(w, 200, map[string]any{"specialties": out})
	})
	mux.HandleFunc("POST /admin/reindex", admin(func(w http.ResponseWriter, r *http.Request) {
		index, n, err := uc.Reindex(r.Context())
		if err != nil {
			mapError(w, err)
			return
		}
		slog.InfoContext(r.Context(), "search index rebuilt", "event", "search.reindexed", "index", index, "documents", n)
		sendJSON(w, 200, struct {
			Index     string `json:"index"`
			Documents int    `json:"documents"`
		}{index, n})
	}))
}

func admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(RoleHeader) != "admin" {
			sendError(w, 403, "FORBIDDEN", "This operation requires the admin role.")
			return
		}
		next(w, r)
	}
}

type paramError string

func (e paramError) Error() string { return string(e) }

func parseQuery(r *http.Request) (domain.Query, error) {
	v := r.URL.Query()
	q := domain.Query{Q: strings.TrimSpace(v.Get("q")), Specialty: v.Get("specialty"), Sort: v.Get("sort"), Page: 1, Size: domain.DefaultSize}
	if len(q.Q) > 100 {
		return q, paramError("q must have at most 100 characters")
	}
	if s := v.Get("acceptsMinors"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return q, paramError("acceptsMinors must be true or false")
		}
		q.AcceptsMinors = &b
	}
	for name, dst := range map[string]*int{"page": &q.Page, "size": &q.Size} {
		if s := v.Get(name); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return q, paramError(name + " must be an integer")
			}
			*dst = n
		}
	}
	return q, q.Validate()
}

func mapError(w http.ResponseWriter, err error) {
	var inv *domain.InvalidError
	switch {
	case errors.As(err, &inv):
		sendError(w, 422, "VALIDATION_ERROR", inv.Msg)
	case errors.Is(err, app.ErrNotFound):
		sendError(w, 404, "NOT_FOUND", "Doctor not found.")
	case errors.Is(err, app.ErrUnknownSpecialty):
		sendError(w, 422, "UNKNOWN_SPECIALTY", "One or more specialties do not exist; see GET /specialties.")
	case errors.Is(err, app.ErrSearchUnavailable):
		sendError(w, 503, "SEARCH_UNAVAILABLE", "Doctor search is temporarily unavailable. Please try again in a few minutes.")
	default:
		slog.Error("unexpected error", "event", "http.internal_error", "error", err.Error())
		sendError(w, 500, "INTERNAL", "Unexpected error.")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		sendError(w, 400, "INVALID_JSON", "Invalid JSON or unknown fields.")
		return false
	}
	return true
}

func sendJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func sendError(w http.ResponseWriter, status int, code, msg string) {
	sendJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
