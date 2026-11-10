package access

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"portal-paciente-clinica/services/historia-clinica/internal/service"
	"strings"
	"testing"
	"time"
)

func TestClinicalAccessUsesCurrentVerificationAndUncancelledRelationship(t *testing.T) {
	enabled := true
	related := true
	relationStatus := 200
	profiles := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/internal/patients/")
		role, birth, responsible := "patient", "1985-01-01", ""
		if id == "doctor" {
			role = "doctor"
		}
		if id == "child" {
			birth = "2015-01-01"
			responsible = "adult"
		}
		json.NewEncoder(w).Encode(map[string]any{"patientId": id, "role": role, "enabled": enabled, "birthDate": birth, "responsiblePatientId": responsible})
	}))
	defer profiles.Close()
	relations := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(relationStatus)
		json.NewEncoder(w).Encode(map[string]bool{"allowed": related})
	}))
	defer relations.Close()
	p := New(profiles.URL, relations.URL, strings.Repeat("k", 32))
	p.Now = func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }
	for _, c := range [][3]string{{"patient", "adult", "adult"}, {"patient", "adult", "child"}, {"doctor", "doctor", "adult"}} {
		if err := p.Authorize(context.Background(), c[0], c[1], c[2]); err != nil {
			t.Fatalf("authorized relation %v: %v", c, err)
		}
	}
	if err := p.Authorize(context.Background(), "reception", "rec", "adult"); !errors.Is(err, service.ErrForbidden) {
		t.Fatal("reception obtained clinical access")
	}
	if err := p.Authorize(context.Background(), "patient", "other", "child"); !errors.Is(err, service.ErrForbidden) {
		t.Fatal("unrelated adult obtained clinical access")
	}
	enabled = false
	if err := p.Authorize(context.Background(), "patient", "adult", "adult"); !errors.Is(err, service.ErrForbidden) {
		t.Fatal("disabled account obtained clinical access")
	}
	enabled = true
	related = false
	if err := p.Authorize(context.Background(), "doctor", "doctor", "adult"); !errors.Is(err, service.ErrForbidden) {
		t.Fatal("cancelled-only relation obtained access")
	}
	related = true
	relationStatus = 503
	if err := p.Authorize(context.Background(), "doctor", "doctor", "adult"); !errors.Is(err, service.ErrAccessUnavailable) {
		t.Fatalf("dependency outage did not fail closed: %v", err)
	}
}
