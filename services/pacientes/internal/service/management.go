package service

import (
	"context"
	"errors"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"time"
)

// These mutations are only called by the reception/admin HTTP routes. A
// responsible relationship is verified here before persisting it.
type ManagementStore interface {
	ListPatients(context.Context, bool, int) ([]repository.Patient, error)
	SetVerification(context.Context, string, bool) error
	SetResponsible(context.Context, string, string) error
}

var ErrManagementUnavailable = errors.New("patient management unavailable")

func (s *Service) Patients(ctx context.Context, pending bool, page int, query ...string) ([]repository.Patient, error) {
	store, ok := s.Store.(ManagementStore)
	if !ok {
		return nil, ErrManagementUnavailable
	}
	if len(query) > 0 && query[0] != "" {
		if len(query[0]) > 120 {
			return nil, &ValidationError{"La búsqueda admite hasta 120 caracteres."}
		}
		if search, ok := s.Store.(interface {
			SearchPatients(context.Context, bool, int, string) ([]repository.Patient, error)
		}); ok {
			return search.SearchPatients(ctx, pending, page, query[0])
		}
	}
	return store.ListPatients(ctx, pending, page)
}
func (s *Service) Verify(ctx context.Context, id string, enabled bool) error {
	p, err := s.Store.ByID(ctx, id)
	if err != nil {
		return err
	}
	if p.Role != "patient" {
		return &ValidationError{"Only patients may be verified"}
	}
	store, ok := s.Store.(ManagementStore)
	if !ok {
		return ErrManagementUnavailable
	}
	return store.SetVerification(ctx, id, enabled)
}
func adult(birth, now time.Time) bool {
	if birth.IsZero() {
		return false
	}
	civil := time.Date(birth.Year(), birth.Month(), birth.Day(), 0, 0, 0, 0, clinicZone)
	return !civil.AddDate(18, 0, 0).After(now.In(clinicZone))
}
func (s *Service) SetResponsible(ctx context.Context, id, responsible string) error {
	child, err := s.Store.ByID(ctx, id)
	if err != nil {
		return err
	}
	if child.Role != "patient" || child.BirthDate.IsZero() || adult(child.BirthDate, s.Now()) {
		return &ValidationError{"The patient must be a minor with a known birth date"}
	}
	if responsible != "" {
		guardian, err := s.Store.ByID(ctx, responsible)
		if err != nil {
			return err
		}
		if guardian.Role != "patient" || !guardian.Enabled || !adult(guardian.BirthDate, s.Now()) || guardian.ID == id {
			return &ValidationError{"The responsible adult must be verified and at least 18 years old"}
		}
	}
	store, ok := s.Store.(ManagementStore)
	if !ok {
		return ErrManagementUnavailable
	}
	return store.SetResponsible(ctx, id, responsible)
}
