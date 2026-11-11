package directory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/internal/platform/patientclient"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// HTTP reads identities and verification from their owner; Turnos owns the
// agenda defaults. Profile changes in Medicos (including deactivation) apply
// to the next reservation, without inventing an enabled patient on failures.
type HTTP struct {
	Patients   *patientclient.Client
	MedicosURL string
	Client     *http.Client
}

func New(patients, medicos, key string) *HTTP {
	return &HTTP{Patients: patientclient.New(patients, key), MedicosURL: strings.TrimRight(medicos, "/"), Client: obs.Client(400 * time.Millisecond)}
}
func (d *HTTP) Patient(ctx context.Context, id string) (domain.Patient, error) {
	p, err := d.Patients.Profile(ctx, id)
	if errors.Is(err, patientclient.ErrNotFound) {
		return domain.Patient{}, app.ErrUnknownPatient
	}
	if err != nil {
		return domain.Patient{}, app.ErrDirectoryUnavailable
	}
	birth, _ := time.ParseInLocation(time.DateOnly, p.BirthDate, domain.ClinicZone)
	return domain.Patient{ID: p.PatientID, Enabled: p.Enabled, BirthDate: birth, ResponsibleID: p.ResponsibleID, Role: p.Role}, nil
}
func (d *HTTP) Doctor(ctx context.Context, id string) (domain.Doctor, error) {
	doctor, err := Agenda(ctx, id)
	if err != nil {
		return doctor, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", d.MedicosURL+"/doctors/"+url.PathEscape(id), nil)
	if err != nil {
		return domain.Doctor{}, app.ErrDirectoryUnavailable
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return domain.Doctor{}, app.ErrDirectoryUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return domain.Doctor{}, app.ErrUnknownDoctor
	}
	if resp.StatusCode != 200 {
		return domain.Doctor{}, app.ErrDirectoryUnavailable
	}
	var p struct {
		ID            string   `json:"id"`
		Active        bool     `json:"active"`
		Specialties   []string `json:"specialties"`
		AcceptsMinors bool     `json:"acceptsMinors"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&p) != nil || p.ID != id {
		return domain.Doctor{}, app.ErrDirectoryUnavailable
	}
	if !p.Active {
		return domain.Doctor{}, app.ErrUnknownDoctor
	}
	doctor.AcceptsMinors = p.AcceptsMinors
	if len(p.Specialties) > 0 && !slices.Contains(p.Specialties, doctor.Specialty) {
		doctor.Specialty = p.Specialties[0]
	}
	return doctor, nil
}
func (d *HTTP) Doctors(ctx context.Context) ([]domain.Doctor, error) {
	seeds := Agendas(ctx)
	out := []domain.Doctor{}
	for _, seed := range seeds {
		doctor, err := d.Doctor(ctx, seed.ID)
		if errors.Is(err, app.ErrUnknownDoctor) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, doctor)
	}
	return out, nil
}
