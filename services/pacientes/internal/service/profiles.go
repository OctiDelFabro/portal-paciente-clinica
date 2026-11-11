package service

import (
	"context"
	"errors"
	"net/mail"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrForbidden = errors.New("patient access denied")
var clinicZone = time.FixedZone("ART", -3*60*60)

type PatientFields struct {
	Email          string `json:"email"`
	FullName       string `json:"fullName"`
	DocumentNumber string `json:"documentNumber"`
	BirthDate      string `json:"birthDate"`
}

func (s *Service) fields(in *PatientFields) (time.Time, error) {
	in.FullName = strings.TrimSpace(in.FullName)
	in.DocumentNumber = strings.ToUpper(strings.TrimSpace(in.DocumentNumber))
	in.Email = normalizeEmail(in.Email)
	if in.Email != "" {
		a, e := mail.ParseAddress(in.Email)
		if e != nil || a.Address != in.Email || len(in.Email) > 254 {
			return time.Time{}, &ValidationError{"Correo electrónico inválido."}
		}
	}
	if in.FullName == "" || utf8.RuneCountInString(in.FullName) > 120 {
		return time.Time{}, &ValidationError{"El nombre admite entre 1 y 120 caracteres."}
	}
	if !documentPattern.MatchString(in.DocumentNumber) {
		return time.Time{}, &ValidationError{"El documento admite de 6 a 20 letras o números."}
	}
	birth, e := time.ParseInLocation(time.DateOnly, in.BirthDate, clinicZone)
	if e != nil || birth.After(s.Now().In(clinicZone)) {
		return time.Time{}, &ValidationError{"La fecha de nacimiento no puede ser futura."}
	}
	return birth, nil
}
func (s *Service) CreatePatient(ctx context.Context, in PatientFields, password string, enabled bool) (repository.Patient, error) {
	birth, e := s.fields(&in)
	if e != nil {
		return repository.Patient{}, e
	}
	hash := ""
	if password != "" {
		if in.Email == "" {
			return repository.Patient{}, &ValidationError{"El acceso al portal requiere un correo electrónico."}
		}
		if reason := validPassword(password); reason != "" {
			return repository.Patient{}, &ValidationError{reason}
		}
		hash, e = s.hash(password)
		if e != nil {
			return repository.Patient{}, e
		}
	}
	p := repository.Patient{ID: "pat-" + s.NewID(), Email: in.Email, PasswordHash: hash, FullName: in.FullName, DocumentNumber: in.DocumentNumber, BirthDate: birth, Enabled: enabled, Role: "patient", CreatedAt: s.Now().UTC()}
	if e := s.Store.Create(ctx, p); e != nil {
		return p, e
	}
	return p, nil
}
func (s *Service) UpdatePatient(ctx context.Context, id string, in PatientFields) (repository.Patient, error) {
	p, e := s.Store.ByID(ctx, id)
	if e != nil {
		return p, e
	}
	if p.Role != "patient" {
		return p, ErrForbidden
	}
	birth, e := s.fields(&in)
	if e != nil {
		return p, e
	}
	store, ok := s.Store.(interface {
		UpdatePatient(context.Context, string, string, string, string, time.Time) error
	})
	if !ok {
		return p, ErrManagementUnavailable
	}
	if e := store.UpdatePatient(ctx, id, in.Email, in.FullName, in.DocumentNumber, birth); e != nil {
		return p, e
	}
	return s.Store.ByID(ctx, id)
}
func (s *Service) Dependents(ctx context.Context, id string) ([]repository.Patient, error) {
	actor, e := s.Store.ByID(ctx, id)
	if e != nil {
		return nil, e
	}
	now := s.Now().In(clinicZone)
	if actor.Role != "patient" || !actor.Enabled || !adult(actor.BirthDate, now) {
		return nil, ErrForbidden
	}
	store, ok := s.Store.(interface {
		Dependents(context.Context, string) ([]repository.Patient, error)
	})
	if !ok {
		return nil, ErrManagementUnavailable
	}
	list, e := store.Dependents(ctx, id)
	if e != nil {
		return nil, e
	}
	out := []repository.Patient{}
	for _, p := range list {
		if p.Enabled && !p.BirthDate.IsZero() && !adult(p.BirthDate, now) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *Service) CanReadContact(ctx context.Context, caller, target, role string) bool {
	if caller == target {
		return true
	}
	if role == "doctor" || role == "reception" || role == "admin" {
		return true
	}
	if role != "patient" {
		return false
	}
	children, e := s.Dependents(ctx, caller)
	if e != nil {
		return false
	}
	for _, p := range children {
		if p.ID == target {
			return true
		}
	}
	return false
}
