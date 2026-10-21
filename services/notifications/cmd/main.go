// Notificaciones: capacidad publica de correos por plantilla y avisos internos
// del portal (arquitectura en capas, ADR-002).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/notifications/internal/api"
	"portal-paciente-clinica/services/notifications/internal/contacts"
	"portal-paciente-clinica/services/notifications/internal/mailer"
	"portal-paciente-clinica/services/notifications/internal/repository"
	"portal-paciente-clinica/services/notifications/internal/service"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err.Error())
	os.Exit(1)
}

func main() {
	obs.NewLogger("notifications")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := obs.InitTracing(ctx, "notifications")
	if err != nil {
		fatal("tracing init failed", err)
	}
	defer shutdown(context.Background())

	keys := map[string]string{}
	for _, item := range strings.Split(os.Getenv("NOTIF_API_KEYS"), ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" && parts[0] != service.PortalConsumer {
			keys[parts[0]] = parts[1]
		}
	}
	if len(keys) == 0 {
		slog.Error("NOTIF_API_KEYS=consumer:key is required")
		os.Exit(1)
	}
	dsn := os.Getenv("NOTIF_DB_DSN")
	if dsn == "" {
		slog.Error("NOTIF_DB_DSN is required")
		os.Exit(1)
	}
	var repo *repository.Repo
	for attempt := 1; ; attempt++ {
		repo, err = repository.Open(ctx, dsn)
		if err == nil {
			break
		}
		if attempt == 30 || ctx.Err() != nil {
			fatal("database unavailable", err)
		}
		slog.Warn("waiting for database", "event", "db.waiting", "attempt", attempt)
		time.Sleep(2 * time.Second)
	}

	svc := &service.Service{
		Repo: repo, Contacts: contacts.Seed{}, Now: time.Now, NewID: obs.NewID, TTL: 24 * time.Hour,
		Mailer:  mailer.SMTP{Addr: env("SMTP_ADDR", "127.0.0.1:1025"), From: env("MAIL_FROM", "no-reply@portal-clinica.example.test")},
		Breaker: &service.Breaker{Threshold: 5, Cooldown: 30 * time.Second},
	}
	go svc.RunDelivery(ctx, time.Second)
	consumer := &api.Consumer{URL: env("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/"), Service: svc}
	go consumer.Run(ctx)

	mux := http.NewServeMux()
	(&api.HTTP{Service: svc, Keys: keys, Rate: 60, Health: func() error { return repo.Ping(ctx) }}).Register(mux)
	mux.Handle("GET /metrics", obs.MetricsHandler())

	addr := env("HTTP_ADDR", "127.0.0.1:8085")
	server := &http.Server{Addr: addr, Handler: obs.Middleware("notifications", mux), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(sctx)
	}()
	slog.Info("notifications listening", "event", "service.started", "addr", addr, "contract", "1.1.0")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("http server failed", err)
	}
}
