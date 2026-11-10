package directory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"portal-paciente-clinica/services/turnos/internal/app"
	"strings"
	"testing"
)

func TestPatientVerificationComesFromOwnerAndFailsClosed(t *testing.T) {
	enabled := false
	status := 200
	key := strings.Repeat("k", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Key") != key {
			t.Error("missing internal key")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"patientId": "pat-new", "role": "patient", "enabled": enabled})
	}))
	defer server.Close()
	d := New(server.URL, server.URL, key)
	p, err := d.Patient(context.Background(), "pat-new")
	if err != nil || p.Enabled {
		t.Fatalf("unverified patient enabled: %+v %v", p, err)
	}
	enabled = true
	p, err = d.Patient(context.Background(), "pat-new")
	if err != nil || !p.Enabled {
		t.Fatal("owner verification not observed")
	}
	status = 503
	if _, err := d.Patient(context.Background(), "pat-new"); !errors.Is(err, app.ErrDirectoryUnavailable) {
		t.Fatalf("outage must not authorize: %v", err)
	}
	status = 404
	if _, err := d.Patient(context.Background(), "pat-new"); !errors.Is(err, app.ErrUnknownPatient) {
		t.Fatalf("unknown patient accepted: %v", err)
	}
}
