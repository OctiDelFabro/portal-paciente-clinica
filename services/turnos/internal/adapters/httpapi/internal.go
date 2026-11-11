package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type RelationshipReader interface {
	HasRelationship(context.Context, string, string) (bool, error)
}

func RegisterInternal(mux *http.ServeMux, reader RelationshipReader, key string) {
	if get, ok := reader.(interface {
		Get(context.Context, string) (domain.Appointment, error)
	}); ok {
		mux.HandleFunc("GET /internal/appointments/{id}", func(w http.ResponseWriter, r *http.Request) {
			expected, got := sha256.Sum256([]byte(key)), sha256.Sum256([]byte(r.Header.Get("X-Service-Key")))
			if len(key) < 32 || subtle.ConstantTimeCompare(expected[:], got[:]) != 1 {
				sendError(w, 401, "UNAUTHORIZED", "Service authentication required.")
				return
			}
			a, e := get.Get(r.Context(), r.PathValue("id"))
			if e != nil {
				mapError(w, e)
				return
			}
			sendJSON(w, 200, toDTO(a))
		})
	}
	mux.HandleFunc("GET /internal/relationships", func(w http.ResponseWriter, r *http.Request) {
		expected, got := sha256.Sum256([]byte(key)), sha256.Sum256([]byte(r.Header.Get("X-Service-Key")))
		if len(key) < 32 || subtle.ConstantTimeCompare(expected[:], got[:]) != 1 {
			sendError(w, 401, "UNAUTHORIZED", "Service authentication required.")
			return
		}
		doctor, patient := r.URL.Query().Get("doctorId"), r.URL.Query().Get("patientId")
		if !patientIDShape.MatchString(doctor) || !patientIDShape.MatchString(patient) {
			sendError(w, 400, "INVALID_REQUEST", "doctorId y patientId son obligatorios.")
			return
		}
		allowed, err := reader.HasRelationship(r.Context(), doctor, patient)
		if err != nil {
			mapError(w, err)
			return
		}
		sendJSON(w, 200, map[string]bool{"allowed": allowed})
	})
}
