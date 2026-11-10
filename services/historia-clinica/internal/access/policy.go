// Package access validates current patient verification and clinical
// relationships against their owners before returning or changing records.
package access

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/internal/platform/patientclient"
	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

type Policy struct {
	Patients *patientclient.Client
	Turnos   []string
	Key      string
	HTTP     *http.Client
	Now      func() time.Time
}

func New(patients, turnos, key string) *Policy {
	client := obs.Client(400 * time.Millisecond)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Policy{Patients: patientclient.New(patients, key), Turnos: strings.Split(turnos, ","), Key: key, HTTP: client, Now: time.Now}
}
func (p *Policy) Authorize(ctx context.Context, role, caller, target string) error {
	if role != "patient" && role != "doctor" {
		return service.ErrForbidden
	}
	actor, err := p.Patients.Profile(ctx, caller)
	if err != nil {
		return service.ErrAccessUnavailable
	}
	if actor.Role != role || !actor.Enabled {
		return service.ErrForbidden
	}
	if role == "patient" && caller == target {
		return nil
	}
	patient, err := p.Patients.Profile(ctx, target)
	if err != nil {
		return service.ErrAccessUnavailable
	}
	if patient.Role != "patient" {
		return service.ErrForbidden
	}
	if role == "patient" {
		childBirth, e1 := time.Parse(time.DateOnly, patient.BirthDate)
		adultBirth, e2 := time.Parse(time.DateOnly, actor.BirthDate)
		if !patient.Enabled || patient.ResponsibleID != caller || e1 != nil || e2 != nil || !childBirth.AddDate(18, 0, 0).After(p.Now()) || adultBirth.AddDate(18, 0, 0).After(p.Now()) {
			return service.ErrForbidden
		}
		return nil
	}
	for _, base := range p.Turnos {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(strings.TrimSpace(base), "/")+"/internal/relationships?"+url.Values{"doctorId": {caller}, "patientId": {target}}.Encode(), nil)
		if err != nil {
			continue
		}
		req.Header.Set("X-Service-Key", p.Key)
		resp, err := p.HTTP.Do(req)
		if err != nil {
			continue
		}
		var relation struct {
			Allowed bool `json:"allowed"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&relation)
		resp.Body.Close()
		if resp.StatusCode != 200 || err != nil {
			continue
		}
		if !relation.Allowed {
			return service.ErrForbidden
		}
		return nil
	}
	return service.ErrAccessUnavailable
}
