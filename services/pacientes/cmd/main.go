// Pacientes: patient identity, self registration and login (layered
// architecture, ADR-002). Issues the HS256 JWT the gateway validates.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/pacientes/internal/api"
	"portal-paciente-clinica/services/pacientes/internal/config"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"portal-paciente-clinica/services/pacientes/internal/service"
)

func fatal(msg string, err error) {
	slog.Error(msg, "error", err.Error())
	os.Exit(1)
}

func main() {
	if len(os.Getenv("INTERNAL_API_KEY")) < 32 {
		slog.Error("INTERNAL_API_KEY must contain at least 32 bytes")
		os.Exit(1)
	}
	obs.NewLogger("pacientes")
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fatal("invalid configuration", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := obs.InitTracing(ctx, "pacientes")
	if err != nil {
		fatal("tracing init failed", err)
	}
	defer shutdown(context.Background())

	var repo *repository.Repo
	// MySQL may take a while to accept connections when the stack starts.
	for attempt := 1; ; attempt++ {
		repo, err = repository.Open(ctx, cfg.DSN)
		if err == nil {
			break
		}
		if attempt == 30 || ctx.Err() != nil {
			fatal("database unavailable", err)
		}
		slog.Warn("waiting for database", "event", "db.waiting", "attempt", attempt)
		time.Sleep(2 * time.Second)
	}

	issuer, err := service.NewIssuer(cfg.Secret, cfg.TokenTTL, time.Now)
	if err != nil {
		fatal("invalid JWT settings", err)
	}
	svc := &service.Service{Store: repo, Tokens: issuer, Now: time.Now, NewID: obs.NewID,
		Limiter: &service.Limiter{Max: 5, Window: 15 * time.Minute}}
	svc.WarmUp()
	if err := svc.SeedDemo(ctx, cfg.DemoPassword); err != nil {
		fatal("demo seed failed", err)
	}

	mux := http.NewServeMux()
	(&api.HTTP{Service: svc, InternalKey: os.Getenv("INTERNAL_API_KEY"), Health: func() error { return repo.Ping(ctx) }}).Register(mux)
	mux.Handle("GET /metrics", obs.MetricsHandler())

	server := &http.Server{Addr: cfg.Addr, Handler: obs.Middleware("pacientes", mux), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(sctx)
	}()
	slog.Info("pacientes listening", "event", "service.started", "addr", cfg.Addr, "token_ttl", cfg.TokenTTL.String())
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("http server failed", err)
	}
}
