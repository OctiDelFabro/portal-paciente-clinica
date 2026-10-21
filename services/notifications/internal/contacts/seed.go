// Package contacts resuelve destinatarios con datos semilla mientras Pacientes
// no esta operativo (limitacion de la Entrega 2). Las direcciones son ficticias.
package contacts

import (
	"context"

	"portal-paciente-clinica/services/notifications/internal/service"
)

type person struct {
	email       string
	responsible string
}

// pat-003 es un menor sin correo propio: solo se notifica a su responsable (RF-12).
var people = map[string]person{
	"pat-001": {email: "paciente1@example.test"},
	"pat-002": {email: "paciente2@example.test"},
	"pat-003": {responsible: "pat-001"},
}

type Seed struct{}

func (Seed) Emails(_ context.Context, patientID string) ([]string, error) {
	p, ok := people[patientID]
	if !ok {
		return nil, service.ErrNoContact
	}
	var out []string
	if p.email != "" {
		out = append(out, p.email)
	}
	if r, ok := people[p.responsible]; ok && r.email != "" {
		out = append(out, r.email)
	}
	if len(out) == 0 {
		return nil, service.ErrNoContact
	}
	return out, nil
}
