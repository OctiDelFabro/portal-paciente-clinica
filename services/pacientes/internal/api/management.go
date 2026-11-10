package api

import (
	"errors"
	"net/http"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"portal-paciente-clinica/services/pacientes/internal/service"
	"strconv"
)

func staff(r *http.Request) bool {
	role := r.Header.Get("X-User-Role")
	return r.Header.Get("X-Patient-Id") != "" && (role == "admin" || role == "reception")
}
func (h *HTTP) registerManagement(mux *http.ServeMux) {
	mux.HandleFunc("GET /patients", func(w http.ResponseWriter, r *http.Request) {
		if !staff(r) {
			sendError(w, 403, "FORBIDDEN", "Reception access required.")
			return
		}
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100000 {
				sendError(w, 400, "INVALID_PAGE", "Invalid page.")
				return
			}
			page = n
		}
		ps, err := h.Service.Patients(r.Context(), r.URL.Query().Get("pending") == "true", page)
		if err != nil {
			h.managementError(w, err)
			return
		}
		next := 0
		if len(ps) > 50 {
			ps = ps[:50]
			next = page + 1
		}
		out := []map[string]any{}
		for _, p := range ps {
			out = append(out, map[string]any{"patientId": p.ID, "fullName": p.FullName, "email": emailPtr(p.Email), "enabled": p.Enabled, "responsiblePatientId": p.ResponsibleID})
		}
		sendJSON(w, 200, map[string]any{"patients": out, "page": page, "nextPage": next})
	})
	mux.HandleFunc("PATCH /patients/{id}/verification", func(w http.ResponseWriter, r *http.Request) {
		if !staff(r) {
			sendError(w, 403, "FORBIDDEN", "Reception access required.")
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			sendError(w, 422, "VALIDATION_ERROR", "enabled is required.")
			return
		}
		if err := h.Service.Verify(r.Context(), r.PathValue("id"), *in.Enabled); err != nil {
			h.managementError(w, err)
			return
		}
		sendJSON(w, 200, map[string]bool{"enabled": *in.Enabled})
	})
	mux.HandleFunc("PATCH /patients/{id}/responsible", func(w http.ResponseWriter, r *http.Request) {
		if !staff(r) {
			sendError(w, 403, "FORBIDDEN", "Reception access required.")
			return
		}
		var in struct {
			Responsible *string `json:"responsiblePatientId"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Responsible == nil {
			sendError(w, 422, "VALIDATION_ERROR", "responsiblePatientId is required; empty string removes the relationship.")
			return
		}
		if err := h.Service.SetResponsible(r.Context(), r.PathValue("id"), *in.Responsible); err != nil {
			h.managementError(w, err)
			return
		}
		sendJSON(w, 200, map[string]string{"responsiblePatientId": *in.Responsible})
	})
}
func (h *HTTP) managementError(w http.ResponseWriter, err error) {
	var invalid *service.ValidationError
	switch {
	case errors.As(err, &invalid):
		sendError(w, 422, "VALIDATION_ERROR", invalid.Reason)
	case errors.Is(err, repository.ErrNotFound):
		sendError(w, 404, "NOT_FOUND", "Patient not found.")
	default:
		sendError(w, 503, "SERVICE_UNAVAILABLE", "Patient management unavailable.")
	}
}
