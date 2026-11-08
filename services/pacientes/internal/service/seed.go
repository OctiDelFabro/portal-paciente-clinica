package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"portal-paciente-clinica/services/pacientes/internal/repository"
)

// demoPatients keeps the IDs of services/turnos (directory seed) and the
// emails of services/notifications (contacts seed). All data is fictitious.
// pat-002 is registered but not enabled by reception (RF-13); pat-003 is a
// minor without email or login whose responsible adult is pat-001 (RF-12).
var demoPatients = []repository.Patient{
	{ID: "pat-001", Email: "paciente1@example.test", FullName: "Lucia Fernandez", DocumentNumber: "30111001", BirthDate: date(1985, 6, 15), Enabled: true, Role: "patient"},
	{ID: "pat-002", Email: "paciente2@example.test", FullName: "Martin Perez", DocumentNumber: "30111002", BirthDate: date(1990, 11, 2), Enabled: false, Role: "patient"},
	{ID: "pat-003", FullName: "Tomas Fernandez", DocumentNumber: "50111003", BirthDate: date(2015, 3, 10), Enabled: true, ResponsibleID: "pat-001", Role: "patient"},
	{ID: "doc-001", Email: "doctor1@example.test", FullName: "Laura Gomez", DocumentNumber: "20111001", BirthDate: date(1980, 4, 12), Enabled: true, Role: "doctor"},
	{ID: "doc-002", Email: "doctor2@example.test", FullName: "Martin Pereyra", DocumentNumber: "20111002", BirthDate: date(1982, 8, 20), Enabled: true, Role: "doctor"},
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// SeedDemo creates the demo patients if missing and, when password is not
// empty, sets it on the demo accounts that have an email. Running it again
// changes nothing unless the password changed.
func (s *Service) SeedDemo(ctx context.Context, password string) error {
	if password == "" {
		slog.InfoContext(ctx, "DEMO_PATIENT_PASSWORD not set: demo patients have no login", "event", "seed.passwords_skipped")
	}
	for _, demo := range demoPatients {
		p, err := s.Store.ByID(ctx, demo.ID)
		if errors.Is(err, repository.ErrNotFound) {
			demo.CreatedAt = s.Now().UTC()
			if err = s.Store.Create(ctx, demo); err != nil && !errors.Is(err, repository.ErrDuplicateID) {
				return err // a concurrent instance may have created it first
			}
			p, err = s.Store.ByID(ctx, demo.ID)
		}
		if err != nil {
			return err
		}
		if p.Role != demo.Role && demo.Role != "" {
			_ = s.Store.SetRole(ctx, p.ID, demo.Role)
			p.Role = demo.Role
		}
		if password == "" || p.Email == "" {
			continue
		}
		if p.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(p.PasswordHash), []byte(password)) == nil {
			continue
		}
		hash, err := s.hash(password)
		if err != nil {
			return err
		}
		if err := s.Store.SetPasswordHash(ctx, p.ID, hash); err != nil {
			return err
		}
	}
	slog.InfoContext(ctx, "demo patients ready", "event", "seed.done", "count", len(demoPatients))
	return nil
}
