package contacts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"portal-paciente-clinica/internal/platform/patientclient"
	"portal-paciente-clinica/services/notifications/internal/service"
	"strings"
	"testing"
)

func TestHTTPContactsResolveNewPatientsAndResponsibleAndKeepOutagesRetryable(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		id := strings.TrimPrefix(r.URL.Path, "/internal/patients/")
		email := "new@example.test"
		responsible := "adult"
		birth := "2015-03-10"
		if id == "adult" {
			email = "adult@example.test"
			responsible = ""
			birth = "1985-06-15"
		}
		if id == "no-mail" {
			email = ""
			responsible = ""
		}
		json.NewEncoder(w).Encode(map[string]any{"patientId": id, "role": "patient", "enabled": true, "email": email, "responsiblePatientId": responsible, "birthDate": birth})
	}))
	defer server.Close()
	c := HTTP{Patients: patientclient.New(server.URL, strings.Repeat("k", 32))}
	emails, err := c.Emails(context.Background(), "new-minor")
	if err != nil || len(emails) != 2 || emails[0] != "new@example.test" || emails[1] != "adult@example.test" {
		t.Fatalf("contacts: %v %v", emails, err)
	}
	if _, err := c.Emails(context.Background(), "no-mail"); !errors.Is(err, service.ErrNoContact) {
		t.Fatal("successful empty lookup should be explicit")
	}
	status = 503
	if _, err := c.Emails(context.Background(), "new-minor"); err == nil || errors.Is(err, service.ErrNoContact) {
		t.Fatal("outage was silently discarded")
	}
}
