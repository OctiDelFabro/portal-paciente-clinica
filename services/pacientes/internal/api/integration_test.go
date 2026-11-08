//go:build integration

// Integration tests against a real MySQL (brief 2.11).
// Requires Docker: go test -tags integration ./services/pacientes/...
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"golang.org/x/crypto/bcrypt"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"portal-paciente-clinica/services/pacientes/internal/service"
)

func startRepo(t *testing.T) *repository.Repo {
	t.Helper()
	ctx := context.Background()
	c, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("pacientes"), mysql.WithUsername("pacientes"), mysql.WithPassword("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, _ := c.ConnectionString(ctx)
	repo, err := repository.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func realService(t *testing.T, repo *repository.Repo) *service.Service {
	t.Helper()
	issuer, err := service.NewIssuer("0123456789abcdef0123456789abcdef", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return &service.Service{Store: repo, Tokens: issuer, Now: time.Now, NewID: obs.NewID, Cost: bcrypt.MinCost,
		Limiter: &service.Limiter{Max: 5, Window: 15 * time.Minute}}
}

func TestRegisterLoginAndProfileAgainstMySQL(t *testing.T) {
	repo := startRepo(t)
	svc := realService(t, repo)
	for i := 0; i < 2; i++ { // seeding twice must be idempotent
		if err := svc.SeedDemo(context.Background(), "demo-pass-1"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM patients WHERE id IN ('pat-001','pat-002','pat-003')").Scan(&count)
	if count != 3 {
		t.Fatalf("want 3 demo patients, got %d", count)
	}
	mux := http.NewServeMux()
	(&HTTP{Service: svc, Health: func() error { return repo.Ping(context.Background()) }}).Register(mux)

	rec := call(mux, "POST", "/auth/register", registerBody, nil)
	if rec.Code != 201 {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if rec := call(mux, "POST", "/auth/register", strings.Replace(registerBody, "new@", "NEW@", 1), nil); rec.Code != 409 {
		t.Fatalf("duplicate email must be 409, got %d", rec.Code)
	}
	var stored string
	_ = repo.DB.QueryRow("SELECT password_hash FROM patients WHERE email = 'new@example.test'").Scan(&stored)
	if !strings.HasPrefix(stored, "$2") {
		t.Fatalf("stored value is not a bcrypt hash")
	}
	if rec := call(mux, "POST", "/auth/login", `{"email":"new@example.test","password":"s3cret-pass"}`, nil); rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	if rec := call(mux, "GET", "/me", "", map[string]string{"X-Patient-Id": "pat-003"}); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"birthDate":"2015-03-10"`) {
		t.Fatalf("minor profile: %d %s", rec.Code, rec.Body)
	}
	if rec := call(mux, "GET", "/healthz", "", nil); rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
}

// Concurrent registrations with the same email: the unique index lets exactly one win.
func TestConcurrentRegistrationSameEmailCreatesOnePatient(t *testing.T) {
	repo := startRepo(t)
	svc := realService(t, repo)
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Register(context.Background(), service.RegisterInput{Email: "race@example.test", Password: "s3cret-pass",
				FullName: "Race", DocumentNumber: "40999888", BirthDate: "1990-01-01"})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, repository.ErrEmailTaken):
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("want exactly one registration, got %d", ok)
	}
}
