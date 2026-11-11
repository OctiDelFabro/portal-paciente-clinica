// Package contacts resolves notification recipients from Pacientes (contacts.HTTP).
package contacts

import (
	"context"
	"portal-paciente-clinica/internal/platform/patientclient"
	"portal-paciente-clinica/services/notifications/internal/service"
	"time"
)

// HTTP resolves the current verified patient and responsible adult. An outage
// is retryable; only a successful lookup without addresses is ErrNoContact.
type HTTP struct{ Patients *patientclient.Client }

func (h HTTP) Emails(ctx context.Context, id string) ([]string, error) {
	p, err := h.Patients.Profile(ctx, id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(email *string) {
		if email != nil && *email != "" && !seen[*email] {
			seen[*email] = true
			out = append(out, *email)
		}
	}
	add(p.Email)
	birth, birthErr := time.Parse(time.DateOnly, p.BirthDate)
	if p.ResponsibleID != "" && birthErr == nil && birth.AddDate(18, 0, 0).After(time.Now()) {
		r, err := h.Patients.Profile(ctx, p.ResponsibleID)
		if err != nil {
			return nil, err
		}
		adultBirth, adultErr := time.Parse(time.DateOnly, r.BirthDate)
		if r.Enabled && r.Role == "patient" && adultErr == nil && !adultBirth.AddDate(18, 0, 0).After(time.Now()) {
			add(r.Email)
		}
	}
	if len(out) == 0 {
		return nil, service.ErrNoContact
	}
	return out, nil
}
