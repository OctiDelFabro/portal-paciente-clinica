// Package service holds the Pacientes use cases: self registration, login,
// profile and contact lookup (layered architecture, ADR-002).
package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"portal-paciente-clinica/services/pacientes/internal/repository"
)

// Store is the persistence port implemented by repository.Repo.
type Store interface {
	Create(ctx context.Context, p repository.Patient) error
	ByEmail(ctx context.Context, email string) (repository.Patient, error)
	ByID(ctx context.Context, id string) (repository.Patient, error)
	SetPasswordHash(ctx context.Context, id, hash string) error
}

const (
	MinPasswordLen   = 8
	maxPasswordBytes = 72 // bcrypt ignores anything longer
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	documentPattern       = regexp.MustCompile(`^[A-Za-z0-9]{6,20}$`)
)

type ValidationError struct{ Reason string }

func (e *ValidationError) Error() string { return e.Reason }

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many failed logins, retry in %s", e.RetryAfter)
}

type Service struct {
	Store   Store
	Tokens  *Issuer
	Limiter *Limiter
	Now     func() time.Time
	NewID   func() string
	Cost    int // bcrypt cost; 0 means bcrypt.DefaultCost

	dummyOnce sync.Once
	dummyHash []byte
}

type RegisterInput struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	FullName       string `json:"fullName"`
	DocumentNumber string `json:"documentNumber"`
	BirthDate      string `json:"birthDate"`
}

type Session struct {
	AccessToken string
	ExpiresIn   int
	Patient     repository.Patient
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (s *Service) cost() int {
	if s.Cost == 0 {
		return bcrypt.DefaultCost
	}
	return s.Cost
}

func (s *Service) hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), s.cost())
	return string(h), err
}

func validPassword(password string) string {
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return fmt.Sprintf("password must have at least %d characters", MinPasswordLen)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Sprintf("password must have at most %d bytes", maxPasswordBytes)
	}
	return ""
}

func (s *Service) validate(in *RegisterInput) (time.Time, error) {
	in.Email, in.FullName, in.DocumentNumber = normalizeEmail(in.Email), strings.TrimSpace(in.FullName), strings.TrimSpace(in.DocumentNumber)
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email || len(in.Email) > 254 {
		return time.Time{}, &ValidationError{"email must be a plain address such as name@example.com"}
	}
	if reason := validPassword(in.Password); reason != "" {
		return time.Time{}, &ValidationError{reason}
	}
	if in.FullName == "" || utf8.RuneCountInString(in.FullName) > 120 {
		return time.Time{}, &ValidationError{"fullName is required (at most 120 characters)"}
	}
	if !documentPattern.MatchString(in.DocumentNumber) {
		return time.Time{}, &ValidationError{"documentNumber must have 6 to 20 letters or digits"}
	}
	birth, err := time.Parse(time.DateOnly, in.BirthDate)
	if err != nil || birth.After(s.Now()) {
		return time.Time{}, &ValidationError{"birthDate must be a past date in YYYY-MM-DD format"}
	}
	return birth, nil
}

// Register creates a self-registered patient pending verification by
// reception (RF-13): the account exists but is not enabled.
func (s *Service) Register(ctx context.Context, in RegisterInput) (string, error) {
	birth, err := s.validate(&in)
	if err != nil {
		return "", err
	}
	hash, err := s.hash(in.Password)
	if err != nil {
		return "", err
	}
	p := repository.Patient{ID: "pat-" + s.NewID(), Email: in.Email, PasswordHash: hash, FullName: in.FullName,
		DocumentNumber: in.DocumentNumber, BirthDate: birth, Enabled: false, CreatedAt: s.Now().UTC()}
	if err := s.Store.Create(ctx, p); err != nil {
		return "", err
	}
	return p.ID, nil
}

// Login verifies the credentials and issues an access token. Unknown email
// and wrong password return the same error, and an unknown email still pays a
// bcrypt comparison so response time does not reveal registered addresses.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	key := normalizeEmail(email)
	now := s.Now()
	if wait := s.Limiter.Blocked(key, now); wait > 0 {
		return Session{}, &RateLimitError{RetryAfter: wait}
	}
	p, err := s.Store.ByEmail(ctx, key)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return Session{}, err
	}
	hash := []byte(p.PasswordHash)
	if err != nil || len(hash) == 0 {
		hash = s.dummy()
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || err != nil || p.PasswordHash == "" {
		s.Limiter.Fail(key, now)
		return Session{}, ErrInvalidCredentials
	}
	s.Limiter.Reset(key)
	token, expiresIn, err := s.Tokens.Issue(p.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: token, ExpiresIn: expiresIn, Patient: p}, nil
}

func (s *Service) dummy() []byte {
	s.dummyOnce.Do(func() {
		s.dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), s.cost())
	})
	return s.dummyHash
}

// WarmUp precomputes the dummy hash so the first unknown-email login is not faster.
func (s *Service) WarmUp() { s.dummy() }

func (s *Service) Profile(ctx context.Context, id string) (repository.Patient, error) {
	return s.Store.ByID(ctx, id)
}
