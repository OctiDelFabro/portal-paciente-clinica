// Historia clinica: per-patient clinical timeline on MongoDB, fed by Turnos
// events (layered architecture, ADR-002; persistence ADR-003).
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
	"portal-paciente-clinica/services/historia-clinica/internal/access"
	"portal-paciente-clinica/services/historia-clinica/internal/api"
	"portal-paciente-clinica/services/historia-clinica/internal/repository"
	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Getenv("INTERNAL_API_KEY")) < 32 {
		slog.Error("INTERNAL_API_KEY must contain at least 32 bytes")
		os.Exit(1)
	}
	obs.NewLogger("historia-clinica")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := obs.InitTracing(ctx, "historia-clinica")
	if err != nil {
		slog.Error("tracing init failed", "error", err.Error())
		os.Exit(1)
	}
	defer shutdown(context.Background())

	uri := env("MONGO_URI", "mongodb://127.0.0.1:27017")
	database := env("MONGO_DB", "historia_clinica")
	var repo *repository.Repo
	// MongoDB may take a while to accept connections when the stack starts.
	for attempt := 1; ; attempt++ {
		repo, err = repository.Open(ctx, uri, database)
		if err == nil {
			break
		}
		if attempt == 30 || ctx.Err() != nil {
			slog.Error("database unavailable", "error", err.Error())
			os.Exit(1)
		}
		slog.Warn("waiting for database", "event", "db.waiting", "attempt", attempt)
		time.Sleep(2 * time.Second)
	}
	defer repo.Close(context.Background())

	svc := &service.Service{Access: access.New(env("PACIENTES_URL", "http://127.0.0.1:8082"), env("TURNOS_URLS", "http://127.0.0.1:8081"), os.Getenv("INTERNAL_API_KEY")), Store: repo, Now: time.Now, NewID: obs.NewID}
	consumer := &api.Consumer{URL: env("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/"), Service: svc}
	go consumer.Run(ctx)

	mux := http.NewServeMux()
	(&api.HTTP{Service: svc}).Register(mux)
	mux.Handle("GET /metrics", obs.MetricsHandler())

	addr := env("HTTP_ADDR", "127.0.0.1:8084")
	server := &http.Server{Addr: addr, Handler: obs.Middleware("historia-clinica", mux), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(sctx)
	}()
	slog.Info("historia-clinica listening", "event", "service.started", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("http server failed", "error", err.Error())
		os.Exit(1)
	}
}
