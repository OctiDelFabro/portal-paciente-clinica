package api

import (
	"errors"
	"net/http"
	"portal-paciente-clinica/services/notifications/internal/repository"
	"strconv"
	"time"
)

func (h *HTTP) noticePatient(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	caller, role := r.Header.Get("X-Patient-Id"), r.Header.Get("X-User-Role")
	if caller == "" {
		sendError(w, 401, "UNAUTHORIZED", "Se requiere autenticación.")
		return "", false
	}
	if role != "patient" {
		sendError(w, 403, "FORBIDDEN", "Los avisos pertenecen al paciente y sus responsables verificados.")
		return "", false
	}
	if h.Service.Patients == nil {
		sendError(w, 503, "UNAVAILABLE", "No pudimos comprobar la habilitación.")
		return "", false
	}
	actor, e := h.Service.Patients.Profile(r.Context(), caller)
	if e != nil {
		sendError(w, 503, "UNAVAILABLE", "No pudimos comprobar la habilitación.")
		return "", false
	}
	if !actor.Enabled || actor.Role != "patient" {
		sendError(w, 403, "PATIENT_NOT_ENABLED", "Recepción debe verificar y habilitar tu cuenta.")
		return "", false
	}
	target := r.URL.Query().Get("patientId")
	if target == "" || target == caller {
		return caller, true
	}
	child, e := h.Service.Patients.Profile(r.Context(), target)
	if e != nil {
		sendError(w, 403, "FORBIDDEN", "Vínculo no disponible.")
		return "", false
	}
	zone := time.FixedZone("ART", -3*3600)
	adultBirth, e1 := time.ParseInLocation(time.DateOnly, actor.BirthDate, zone)
	childBirth, e2 := time.ParseInLocation(time.DateOnly, child.BirthDate, zone)
	now := h.Service.Now()
	if e1 != nil || e2 != nil || adultBirth.AddDate(18, 0, 0).After(now) || !childBirth.AddDate(18, 0, 0).After(now) || !child.Enabled || child.Role != "patient" || child.ResponsibleID != caller {
		sendError(w, 403, "FORBIDDEN", "Se requiere un vínculo verificado con el menor.")
		return "", false
	}
	return target, true
}
func (h *HTTP) registerNotices(mux *http.ServeMux) {
	mux.HandleFunc("GET /notices", func(w http.ResponseWriter, r *http.Request) {
		patient, ok := h.noticePatient(w, r)
		if !ok {
			return
		}
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 100000 {
				sendError(w, 400, "INVALID_PAGE", "Página inválida.")
				return
			}
			page = n
		}
		list, more, e := h.Service.Repo.Notices(r.Context(), patient, page)
		if e != nil {
			sendError(w, 503, "UNAVAILABLE", "No pudimos traer los avisos.")
			return
		}
		next := 0
		if more {
			next = page + 1
		}
		sendJSON(w, 200, map[string]any{"notices": list, "page": page, "nextPage": next})
	})
	mux.HandleFunc("POST /notices/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		patient, ok := h.noticePatient(w, r)
		if !ok {
			return
		}
		if e := h.Service.Repo.ReadNotice(r.Context(), r.PathValue("id"), patient, h.Service.Now().UTC()); e != nil {
			if errors.Is(e, repository.ErrNotFound) {
				sendError(w, 404, "NOT_FOUND", "Aviso no encontrado.")
			} else {
				sendError(w, 503, "UNAVAILABLE", "No pudimos actualizar el aviso.")
			}
			return
		}
		sendJSON(w, 200, map[string]bool{"read": true})
	})
}
