package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"portal-paciente-clinica/services/pacientes/internal/repository"
)

const testSecret = "0123456789abcdef0123456789abcdef"

type fakeStore struct {
	mu       sync.Mutex
	byID     map[string]repository.Patient
	failNext error
}

func newFakeStore() *fakeStore { return &fakeStore{byID: map[string]repository.Patient{}} }

func (f *fakeStore) Create(_ context.Context, p repository.Patient) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	if _, ok := f.byID[p.ID]; ok {
		return repository.ErrDuplicateID
	}
	for _, other := range f.byID {
		if p.Email != "" && other.Email == p.Email {
			return repository.ErrEmailTaken
		}
	}
	f.byID[p.ID] = p
	return nil
}

func (f *fakeStore) ByEmail(_ context.Context, email string) (repository.Patient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.byID {
		if p.Email == email {
			return p, nil
		}
	}
	return repository.Patient{}, repository.ErrNotFound
}

func (f *fakeStore) ByID(_ context.Context, id string) (repository.Patient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return repository.Patient{}, repository.ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) SetPasswordHash(_ context.Context, id, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return repository.ErrNotFound
	}
	p.PasswordHash = hash
	f.byID[id] = p
	return nil
}

func (f *fakeStore) SetRole(_ context.Context, id, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return repository.ErrNotFound
	}
	p.Role = role
	f.byID[id] = p
	return nil
}

var fixedNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, store *fakeStore) (*Service, *time.Time) {
	t.Helper()
	now := fixedNow
	clock := func() time.Time { return now }
	issuer, err := NewIssuer(testSecret, time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	return &Service{
		Store: store, Tokens: issuer, Now: clock, Cost: bcrypt.MinCost,
		NewID:   func() string { n++; return "id-" + string(rune('0'+n)) },
		Limiter: &Limiter{Max: 5, Window: 15 * time.Minute},
	}, &now
}

func validInput() RegisterInput {
	return RegisterInput{Email: "Ana@Example.test ", Password: "s3cret-pass", FullName: "Ana Test", DocumentNumber: "30111222", BirthDate: "1990-05-01"}
}

func TestRegisterStoresOnlyBcryptHashAndPendingPatient(t *testing.T) {
	store := newFakeStore()
	svc, _ := newTestService(t, store)
	id, err := svc.Register(context.Background(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	p := store.byID[id]
	if !strings.HasPrefix(id, "pat-") || p.Email != "ana@example.test" || p.Enabled {
		t.Fatalf("unexpected patient %+v", p)
	}
	if p.PasswordHash == "s3cret-pass" || bcrypt.CompareHashAndPassword([]byte(p.PasswordHash), []byte("s3cret-pass")) != nil {
		t.Fatal("password must be stored as a bcrypt hash")
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore())
	if _, err := svc.Register(context.Background(), validInput()); err != nil {
		t.Fatal(err)
	}
	in := validInput()
	in.Email = "ana@example.test"
	if _, err := svc.Register(context.Background(), in); !errors.Is(err, repository.ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	cases := map[string]func(*RegisterInput){
		"short password":    func(in *RegisterInput) { in.Password = "1234567" },
		"password too long": func(in *RegisterInput) { in.Password = strings.Repeat("a", 73) },
		"bad email":         func(in *RegisterInput) { in.Email = "not-an-email" },
		"missing name":      func(in *RegisterInput) { in.FullName = " " },
		"bad document":      func(in *RegisterInput) { in.DocumentNumber = "12" },
		"bad birth date":    func(in *RegisterInput) { in.BirthDate = "01/05/1990" },
		"future birth date": func(in *RegisterInput) { in.BirthDate = "2030-01-01" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newTestService(t, newFakeStore())
			in := validInput()
			mutate(&in)
			var verr *ValidationError
			if _, err := svc.Register(context.Background(), in); !errors.As(err, &verr) {
				t.Fatalf("want ValidationError, got %v", err)
			}
		})
	}
	svc, _ := newTestService(t, newFakeStore())
	in := validInput()
	in.Password = "12345678"
	if _, err := svc.Register(context.Background(), in); err != nil {
		t.Fatalf("8-char password must be accepted: %v", err)
	}
}

func TestLoginIssuesSignedTokenWithClaims(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore())
	id, _ := svc.Register(context.Background(), validInput())
	session, err := svc.Login(context.Background(), "ANA@example.test", "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if session.ExpiresIn != 3600 || session.Patient.ID != id {
		t.Fatalf("unexpected session %+v", session)
	}
	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(session.AccessToken, claims, func(*jwt.Token) (any, error) { return []byte(testSecret), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithTimeFunc(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"sub": id, "role": "patient", "iss": "pacientes", "iat": float64(fixedNow.Unix()), "exp": float64(fixedNow.Add(time.Hour).Unix())}
	for k, v := range want {
		if claims[k] != v {
			t.Errorf("claim %s = %v, want %v", k, claims[k], v)
		}
	}
}

func TestLoginSameErrorForUnknownEmailAndWrongPassword(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore())
	_, _ = svc.Register(context.Background(), validInput())
	_, errUnknown := svc.Login(context.Background(), "nobody@example.test", "s3cret-pass")
	_, errWrong := svc.Login(context.Background(), "ana@example.test", "wrong-pass")
	if !errors.Is(errUnknown, ErrInvalidCredentials) || !errors.Is(errWrong, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials for both, got %v / %v", errUnknown, errWrong)
	}
}

func TestLoginLocksEmailAfterFiveFailuresUntilWindowEnds(t *testing.T) {
	svc, now := newTestService(t, newFakeStore())
	_, _ = svc.Register(context.Background(), validInput())
	for i := 0; i < 5; i++ {
		if _, err := svc.Login(context.Background(), "ana@example.test", "wrong-pass"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	var rl *RateLimitError
	if _, err := svc.Login(context.Background(), "ana@example.test", "s3cret-pass"); !errors.As(err, &rl) || rl.RetryAfter != 15*time.Minute {
		t.Fatalf("want RateLimitError 15m, got %v", err)
	}
	*now = now.Add(15 * time.Minute)
	if _, err := svc.Login(context.Background(), "ana@example.test", "s3cret-pass"); err != nil {
		t.Fatalf("login after window must succeed: %v", err)
	}
}

func TestSuccessfulLoginResetsFailures(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore())
	_, _ = svc.Register(context.Background(), validInput())
	for i := 0; i < 4; i++ {
		_, _ = svc.Login(context.Background(), "ana@example.test", "wrong-pass")
	}
	if _, err := svc.Login(context.Background(), "ana@example.test", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_, _ = svc.Login(context.Background(), "ana@example.test", "wrong-pass")
	}
	if _, err := svc.Login(context.Background(), "ana@example.test", "s3cret-pass"); err != nil {
		t.Fatalf("counter must reset after success: %v", err)
	}
}

func TestNewIssuerRejectsShortSecret(t *testing.T) {
	if _, err := NewIssuer(strings.Repeat("x", 31), time.Hour, time.Now); !errors.Is(err, ErrWeakSecret) {
		t.Fatalf("want ErrWeakSecret, got %v", err)
	}
}

func TestSeedDemoIsIdempotentAndMatchesSharedIDs(t *testing.T) {
	store := newFakeStore()
	svc, _ := newTestService(t, store)
	for i := 0; i < 2; i++ {
		if err := svc.SeedDemo(context.Background(), "demo-pass-1"); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.byID) != 7 {
		t.Fatalf("want 7 demo accounts, got %d", len(store.byID))
	}
	want := map[string]struct {
		email   string
		enabled bool
	}{
		"pat-001": {"paciente1@example.test", true},
		"pat-002": {"paciente2@example.test", false},
		"pat-003": {"", true},
		"doc-001": {"doctor1@example.test", true},
		"doc-002": {"doctor2@example.test", true},
	}
	for id, w := range want {
		p := store.byID[id]
		if p.Email != w.email || p.Enabled != w.enabled {
			t.Errorf("%s: got %+v", id, p)
		}
	}
	if store.byID["pat-003"].ResponsibleID != "pat-001" || store.byID["pat-003"].PasswordHash != "" {
		t.Error("pat-003 is a minor without login, linked to pat-001")
	}
	if _, err := svc.Login(context.Background(), "paciente1@example.test", "demo-pass-1"); err != nil {
		t.Fatalf("demo login: %v", err)
	}
}

func TestSeedDemoWithoutPasswordCreatesProfilesOnly(t *testing.T) {
	store := newFakeStore()
	svc, _ := newTestService(t, store)
	if err := svc.SeedDemo(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(store.byID) != 7 || store.byID["pat-001"].PasswordHash != "" {
		t.Fatalf("profiles must exist without password hashes, got %d profiles", len(store.byID))
	}
	if err := svc.SeedDemo(context.Background(), "demo-pass-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(context.Background(), "paciente2@example.test", "demo-pass-2"); err != nil {
		t.Fatalf("password must be set on a later start: %v", err)
	}
}

func TestDoctorLoginIssuesTokenWithDoctorRole(t *testing.T) {
	store := newFakeStore()
	svc, clock := newTestService(t, store)
	if err := svc.SeedDemo(context.Background(), "doctor-pass-123"); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(context.Background(), "doctor1@example.test", "doctor-pass-123")
	if err != nil {
		t.Fatalf("doctor login failed: %v", err)
	}
	if session.Patient.Role != "doctor" {
		t.Fatalf("want doctor role, got %q", session.Patient.Role)
	}
	// Verify JWT claims
	var claims Claims
	_, err = jwt.ParseWithClaims(session.AccessToken, &claims, func(t *jwt.Token) (any, error) {
		return []byte(testSecret), nil
	}, jwt.WithTimeFunc(func() time.Time { return *clock }))
	if err != nil {
		t.Fatalf("parse jwt: %v", err)
	}
	if claims.Role != "doctor" || claims.Subject != "doc-001" {
		t.Fatalf("unexpected claims: role=%q sub=%q", claims.Role, claims.Subject)
	}
}

func TestContactAndProfile(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore())
	_ = svc.SeedDemo(context.Background(), "")
	p, err := svc.Profile(context.Background(), "pat-001")
	if err != nil || p.Email != "paciente1@example.test" {
		t.Fatalf("profile: %+v %v", p, err)
	}
	if _, err := svc.Profile(context.Background(), "pat-999"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
