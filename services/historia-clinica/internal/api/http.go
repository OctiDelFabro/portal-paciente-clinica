// Package api is the presentation layer of Historia clinica: the HTTP API
// reached through the gateway (/api/historia/*) and the Turnos event consumer.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

// maxNoteBody fits 4000 four-byte characters plus JSON escaping.
const maxNoteBody = 32 << 10

// HTTP trusts X-Patient-Id and X-User-Role: the gateway validates the JWT,
// strips client copies and sets them (cross-unit contract).
type HTTP struct {
	Service *service.Service
}

func (h *HTTP) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /records/me", h.myRecords)
	mux.HandleFunc("POST /records/{appointmentId}/notes", h.addNote)
	mux.HandleFunc("/records/me", methodNotAllowed)
	mux.HandleFunc("/records/{appointmentId}/notes", methodNotAllowed)
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	sendError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed.")
}

func (h *HTTP) health(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.Ping(r.Context()); err != nil {
		sendJSON(w, 503, map[string]string{"service": "historia-clinica", "status": "degraded", "mongo": "down"})
		return
	}
	sendJSON(w, 200, map[string]string{"service": "historia-clinica", "status": "ok", "mongo": "up"})
}

func (h *HTTP) myRecords(w http.ResponseWriter, r *http.Request) {
	patientID := r.Header.Get("X-Patient-Id")
	if patientID == "" {
		sendError(w, 401, "UNAUTHORIZED", "Authenticated patient required.")
		return
	}
	// The query is always scoped to the caller: there is no way to ask for another patient.
	records, err := h.Service.Timeline(r.Context(), patientID)
	if err != nil {
		slog.ErrorContext(r.Context(), "timeline query failed", "event", "records.query_failed", "error", err.Error())
		sendError(w, 503, "SERVICE_UNAVAILABLE", "Clinical records are temporarily unavailable.")
		return
	}
	if records == nil {
		records = []service.Encounter{}
	}
	for i := range records {
		if records[i].Notes == nil {
			records[i].Notes = []service.Note{}
		}
	}
	sendJSON(w, 200, map[string]any{"patientId": patientID, "records": records})
}

func (h *HTTP) addNote(w http.ResponseWriter, r *http.Request) {
	role := r.Header.Get("X-User-Role")
	if role == "" || r.Header.Get("X-Patient-Id") == "" {
		sendError(w, 401, "UNAUTHORIZED", "Authentication required.")
		return
	}
	if role != "doctor" {
		sendError(w, 403, "FORBIDDEN", "Only doctors can add clinical notes.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNoteBody))
	if err != nil {
		sendError(w, 413, "PAYLOAD_TOO_LARGE", "Body exceeds 32 KiB.")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		sendError(w, 400, "INVALID_JSON", "Invalid JSON, unknown fields or wrong types.")
		return
	}
	doctorID := r.Header.Get("X-Patient-Id")
	note, err := h.Service.AddNote(r.Context(), r.PathValue("appointmentId"), doctorID, role, req.Text)
	switch {
	case errors.Is(err, service.ErrInvalidNote):
		sendError(w, 422, "VALIDATION_ERROR", "text is required and must have at most 4000 characters.")
	case errors.Is(err, service.ErrNotFound):
		sendError(w, 404, "NOT_FOUND", "Encounter not found.")
	case errors.Is(err, service.ErrForbidden):
		sendError(w, 403, "FORBIDDEN", "Doctor is not assigned to this appointment.")
	case errors.Is(err, service.ErrCancelled):
		sendError(w, 409, "ENCOUNTER_CANCELLED", "A cancelled appointment does not accept clinical notes.")
	case err != nil:
		slog.ErrorContext(r.Context(), "cannot store note", "event", "note.persist_failed", "error", err.Error())
		sendError(w, 503, "SERVICE_UNAVAILABLE", "The note could not be stored; retry.")
	default:
		sendJSON(w, 201, note)
	}
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sendError(w http.ResponseWriter, status int, code, message string) {
	sendJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
