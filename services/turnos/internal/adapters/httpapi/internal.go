package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

type RelationshipReader interface {
	HasRelationship(context.Context, string, string) (bool, error)
}

func RegisterInternal(mux *http.ServeMux, reader RelationshipReader, key string) {
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
