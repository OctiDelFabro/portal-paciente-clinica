// Package directory provides the production HTTP directory and deterministic
// test fixtures. Only agenda defaults remain seeded in production.
package directory

import (
	"context"
	"sort"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type Seed struct{}

var workdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}

// doctors: appointment duration and weekly schedule (clinic time) that
// availability and authoritative booking both enforce (ADR-007).
// Specialty (as in Medicos seed) selects the consultation energy profile (ADR-009).
var doctors = map[string]domain.Doctor{
	"doc-001": {ID: "doc-001", Duration: 30 * time.Minute, Specialty: "cardiologia", Schedule: domain.Schedule{Weekdays: workdays, Open: 9 * time.Hour, Close: 13 * time.Hour}},
	"doc-002": {ID: "doc-002", Duration: 20 * time.Minute, Specialty: "pediatria", Schedule: domain.Schedule{Weekdays: workdays, Open: 14 * time.Hour, Close: 18 * time.Hour}},
	"doc-003": {ID: "doc-003", Duration: 30 * time.Minute, Specialty: "dermatologia", Schedule: domain.Schedule{Weekdays: workdays, Open: 8 * time.Hour, Close: 12 * time.Hour}},
	"doc-004": {ID: "doc-004", Duration: 20 * time.Minute, Specialty: "cardiologia", Schedule: domain.Schedule{Weekdays: workdays, Open: 15 * time.Hour, Close: 19 * time.Hour}},
}

// pat-002 representa una cuenta registrada pero no habilitada por recepcion (RF-13).
var patients = map[string]bool{
	"pat-001": true,
	"pat-002": false,
	"pat-003": true,
	"pat-004": true,
	"pat-005": true,
	"pat-006": false,
	"pat-007": true,
	"pat-008": true,
}

func (Seed) Doctor(_ context.Context, id string) (domain.Doctor, error) {
	d, ok := doctors[id]
	if !ok {
		return domain.Doctor{}, app.ErrUnknownDoctor
	}
	return d, nil
}

func (Seed) Doctors(context.Context) ([]domain.Doctor, error) {
	out := make([]domain.Doctor, 0, len(doctors))
	for _, d := range doctors {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (Seed) Patient(_ context.Context, id string) (domain.Patient, error) {
	enabled, ok := patients[id]
	if !ok {
		return domain.Patient{}, app.ErrUnknownPatient
	}
	return domain.Patient{ID: id, Enabled: enabled}, nil
}
