package api

import (
	"context"
	"net/http"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"portal-paciente-clinica/services/pacientes/internal/service"
	"strings"
	"testing"
	"time"
)

func (m *memStore) SetVerification(_ context.Context, id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.byID[id]
	p.Enabled = enabled
	m.byID[id] = p
	return nil
}
func (m *memStore) SetResponsible(_ context.Context, id, adult string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.byID[id]
	p.ResponsibleID = adult
	m.byID[id] = p
	return nil
}
func (m *memStore) ListPatients(context.Context, bool, int) ([]repository.Patient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.Patient{}
	for _, p := range m.byID {
		if p.Role == "patient" {
			out = append(out, p)
		}
	}
	return out, nil
}
func TestPrivateProfileAndVerificationRequireDistinctAuthorization(t *testing.T) {
	key := strings.Repeat("k", 32)
	store := &memStore{byID: map[string]repository.Patient{"pat-new": {ID: "pat-new", Role: "patient", Enabled: false, PasswordHash: "never expose"}}}
	svc := &service.Service{Store: store, Now: time.Now}
	mux := http.NewServeMux()
	(&HTTP{Service: svc, InternalKey: key}).Register(mux)
	if r := call(mux, "GET", "/internal/patients/pat-new", "", nil); r.Code != 401 {
		t.Fatal("private directory accepted no key")
	}
	r := call(mux, "GET", "/internal/patients/pat-new", "", map[string]string{"X-Service-Key": key})
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"enabled":false`) || strings.Contains(r.Body.String(), "never expose") {
		t.Fatalf("invalid private profile: %d %s", r.Code, r.Body)
	}
	for _, role := range []string{"patient", "doctor"} {
		r = call(mux, "PATCH", "/patients/pat-new/verification", `{"enabled":true}`, map[string]string{"X-Patient-Id": "caller", "X-User-Role": role})
		if r.Code != 403 {
			t.Fatal("non-staff verified a patient")
		}
	}
	r = call(mux, "PATCH", "/patients/pat-new/verification", `{"enabled":true}`, map[string]string{"X-Patient-Id": "reception", "X-User-Role": "reception"})
	if r.Code != 200 || !store.byID["pat-new"].Enabled {
		t.Fatalf("reception failed: %d %s", r.Code, r.Body)
	}
}

func TestResponsibleRelationshipRequiresAValidMinorAndVerifiedAdult(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	store := &memStore{byID: map[string]repository.Patient{
		"child":       {ID: "child", Role: "patient", Enabled: true, BirthDate: now.AddDate(-10, 0, 0)},
		"adult":       {ID: "adult", Role: "patient", Enabled: true, BirthDate: now.AddDate(-30, 0, 0)},
		"unverified":  {ID: "unverified", Role: "patient", BirthDate: now.AddDate(-30, 0, 0)},
		"other-child": {ID: "other-child", Role: "patient", Enabled: true, BirthDate: now.AddDate(-12, 0, 0)},
	}}
	svc := &service.Service{Store: store, Now: func() time.Time { return now }}
	mux := http.NewServeMux()
	(&HTTP{Service: svc}).Register(mux)
	headers := map[string]string{"X-Patient-Id": "rec-001", "X-User-Role": "reception"}
	for _, adult := range []string{"unverified", "other-child", "child"} {
		r := call(mux, "PATCH", "/patients/child/responsible", `{"responsiblePatientId":"`+adult+`"}`, headers)
		if r.Code != 422 || store.byID["child"].ResponsibleID != "" {
			t.Fatalf("invalid guardian %s was recorded: %d %s", adult, r.Code, r.Body)
		}
	}
	r := call(mux, "PATCH", "/patients/child/responsible", `{"responsiblePatientId":"adult"}`, headers)
	if r.Code != 200 || store.byID["child"].ResponsibleID != "adult" {
		t.Fatalf("verified adult could not be recorded: %d %s", r.Code, r.Body)
	}
	r = call(mux, "PATCH", "/patients/adult/responsible", `{"responsiblePatientId":"adult"}`, headers)
	if r.Code != 422 {
		t.Fatal("adult was treated as minor")
	}
}
