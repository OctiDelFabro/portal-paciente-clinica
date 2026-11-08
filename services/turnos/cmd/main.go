// Turnos: agenda y reservas (arquitectura hexagonal, ADR-002).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"portal-paciente-clinica/internal/platform/apikey"
	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/turnos/internal/adapters/directory"
	"portal-paciente-clinica/services/turnos/internal/adapters/httpapi"
	"portal-paciente-clinica/services/turnos/internal/adapters/mysqlstore"
	"portal-paciente-clinica/services/turnos/internal/adapters/outbox"
	"portal-paciente-clinica/services/turnos/internal/adapters/rediscache"
	"portal-paciente-clinica/services/turnos/internal/app"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// partnerConfig lee la capacidad publicada (ADR-008). Sin claves la API
// responde 401 a todos; un precio, moneda o limite invalido impide iniciar.
func partnerConfig() (map[string]string, app.Pricing, int) {
	keys := apikey.Parse(os.Getenv("TURNOS_PARTNER_KEYS"))
	if len(keys) == 0 {
		slog.Warn("TURNOS_PARTNER_KEYS not set: partner API rejects every request", "event", "partner.disabled")
	}
	cents, err := strconv.ParseInt(env("TURNOS_PARTNER_PRICE_CENTS", "2500000"), 10, 64)
	currency := env("TURNOS_PARTNER_CURRENCY", "ARS")
	rate, rateErr := strconv.Atoi(env("TURNOS_PARTNER_RATE", "60"))
	if err != nil || cents < 0 || !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(currency) || rateErr != nil || rate < 1 {
		slog.Error("invalid TURNOS_PARTNER_PRICE_CENTS, TURNOS_PARTNER_CURRENCY or TURNOS_PARTNER_RATE")
		os.Exit(1)
	}
	return keys, app.Pricing{Cents: cents, Currency: currency}, rate
}

// instanceID identifies this replica in X-Instance (ADR-012); compose sets
// INSTANCE_ID equal to the container hostname so it matches the log field.
func instanceID() string {
	if id := os.Getenv("INSTANCE_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	return host
}

// availabilityQuery applies ADR-007: with CACHE_ENABLED=true (default) reads go
// through Redis cache-aside; with false every read goes to MySQL (k6 baseline).
func availabilityQuery(svc *app.Service) (app.AvailabilityQuery, func()) {
	enabled, err := strconv.ParseBool(env("CACHE_ENABLED", "true"))
	if err != nil {
		slog.Error("invalid CACHE_ENABLED: want true or false")
		os.Exit(1)
	}
	if !enabled {
		slog.Info("availability cache disabled", "event", "cache.disabled")
		return svc, func() {}
	}
	cache := rediscache.New(env("REDIS_ADDR", "127.0.0.1:6379"), svc)
	svc.Cache = cache
	return cache, func() { _ = cache.Close() }
}

func main() {
	obs.NewLogger("turnos")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := obs.InitTracing(ctx, "turnos")
	if err != nil {
		slog.Error("tracing init failed", "error", err.Error())
		os.Exit(1)
	}
	defer shutdown(context.Background())

	dsn := os.Getenv("TURNOS_DB_DSN")
	if dsn == "" {
		slog.Error("TURNOS_DB_DSN is required")
		os.Exit(1)
	}
	var store *mysqlstore.Store
	// MySQL puede tardar en aceptar conexiones al iniciar el entorno.
	for attempt := 1; ; attempt++ {
		store, err = mysqlstore.Open(ctx, dsn)
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

	relay := &outbox.Relay{DB: store.DB, URL: env("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/"), Interval: 500 * time.Millisecond}
	go relay.Run(ctx)

	keys, price, rate := partnerConfig()
	svc := &app.Service{Store: store, Directory: directory.Seed{}, Now: time.Now, NewID: obs.NewID, Price: price}
	query, closeCache := availabilityQuery(svc)
	defer closeCache()
	mux := http.NewServeMux()
	httpapi.Register(mux, svc)
	httpapi.RegisterAvailability(mux, query)
	(&httpapi.Partner{UseCases: svc, Keys: keys, Limiter: &apikey.Limiter{Rate: rate}, Price: price, Now: time.Now}).Register(mux)
	mux.Handle("GET /metrics", obs.MetricsHandler())
	mux.Handle("GET /healthz", httpapi.Health(store.DB.PingContext))

	addr := env("HTTP_ADDR", "127.0.0.1:8081")
	server := &http.Server{Addr: addr, Handler: httpapi.WithInstance(instanceID(), obs.Middleware("turnos", mux)), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(sctx)
	}()
	slog.Info("turnos listening", "event", "service.started", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("http server failed", "error", err.Error())
		os.Exit(1)
	}
}
