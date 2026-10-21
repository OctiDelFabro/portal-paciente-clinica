// Package directory resuelve medicos y pacientes con datos semilla mientras
// los servicios Medicos y Pacientes no estan operativos (limitacion de la
// Entrega 2, documentada en docs/ENTREGA-2.md).
package directory

import (
	"context"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type Seed struct{}

var doctors = map[string]time.Duration{
	"doc-001": 30 * time.Minute,
	"doc-002": 20 * time.Minute,
}

// pat-002 representa una cuenta registrada pero no habilitada por recepcion (RF-13).
var patients = map[string]bool{
	"pat-001": true,
	"pat-002": false,
	"pat-003": true,
}

func (Seed) Doctor(_ context.Context, id string) (domain.Doctor, error) {
	d, ok := doctors[id]
	if !ok {
		return domain.Doctor{}, app.ErrUnknownDoctor
	}
	return domain.Doctor{ID: id, Duration: d}, nil
}

func (Seed) Patient(_ context.Context, id string) (domain.Patient, error) {
	enabled, ok := patients[id]
	if !ok {
		return domain.Patient{}, app.ErrUnknownPatient
	}
	return domain.Patient{ID: id, Enabled: enabled}, nil
}
