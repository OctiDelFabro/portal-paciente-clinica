// Package directory provides the production HTTP directory. Patients come from
// Pacientes and doctor profiles from Medicos; only the agenda below is local.
package directory

import (
	"context"
	"sort"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

var workdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}

// agendas: appointment duration and weekly schedule (clinic time) that
// availability and authoritative booking both enforce (ADR-007). Turnos owns
// schedules; persisted doctor_agendas overrides these initial defaults.
// Specialty (as in Medicos) selects the consultation energy profile (ADR-009).
var agendas = map[string]domain.Doctor{
	"doc-001": {ID: "doc-001", Duration: 30 * time.Minute, Specialty: "cardiologia", Schedule: domain.Schedule{Weekdays: workdays, Open: 9 * time.Hour, Close: 13 * time.Hour}},
	"doc-002": {ID: "doc-002", Duration: 20 * time.Minute, Specialty: "pediatria", Schedule: domain.Schedule{Weekdays: workdays, Open: 14 * time.Hour, Close: 18 * time.Hour}},
	"doc-003": {ID: "doc-003", Duration: 30 * time.Minute, Specialty: "dermatologia", Schedule: domain.Schedule{Weekdays: workdays, Open: 8 * time.Hour, Close: 12 * time.Hour}},
	"doc-004": {ID: "doc-004", Duration: 20 * time.Minute, Specialty: "cardiologia", Schedule: domain.Schedule{Weekdays: workdays, Open: 15 * time.Hour, Close: 19 * time.Hour}},
}

// Agenda returns the configured agenda of a doctor, or app.ErrUnknownDoctor.
func Agenda(_ context.Context, id string) (domain.Doctor, error) {
	d, ok := agendas[id]
	if !ok {
		return domain.Doctor{}, app.ErrUnknownDoctor
	}
	return d, nil
}

// Agendas lists every configured agenda ordered by doctor ID.
func Agendas(context.Context) []domain.Doctor {
	out := make([]domain.Doctor, 0, len(agendas))
	for _, d := range agendas {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
