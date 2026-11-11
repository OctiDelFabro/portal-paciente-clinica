// Package fixture is a deterministic directory for integration tests only.
// Production wires directory.HTTP; nothing in cmd/ imports this package.
package fixture

import (
	"context"

	"portal-paciente-clinica/services/turnos/internal/adapters/directory"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Directory uses the real agendas and the demo patients from main; pat-002
// and pat-006 are registered but not enabled by reception (RF-13).
type Directory struct{}

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

func (Directory) Doctor(ctx context.Context, id string) (domain.Doctor, error) {
	return directory.Agenda(ctx, id)
}

func (Directory) Doctors(ctx context.Context) ([]domain.Doctor, error) {
	return directory.Agendas(ctx), nil
}

func (Directory) Patient(_ context.Context, id string) (domain.Patient, error) {
	enabled, ok := patients[id]
	if !ok {
		return domain.Patient{}, app.ErrUnknownPatient
	}
	return domain.Patient{ID: id, Enabled: enabled}, nil
}
