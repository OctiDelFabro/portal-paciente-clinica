// Gateway: punto de entrada unico del portal. Autentica (JWT emitido por
// Pacientes, ADR-014), limita el trafico por paciente o IP, direcciona,
// balancea Turnos entre instancias sanas (ADR-012), propaga correlacion y
// trazas y aplica los tiempos de espera de ADR-005. No expone API keys al
// frontend.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func sendError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// config is the gateway wiring. turnos is shared by both listeners so that the
// portal and the partner routes use the same instances and health state.
type config struct {
	turnos                            *pool
	notifications, pacientes, medicos string
	historia                          string
	auth                              *authenticator
	limiter                           *tokenBucket
	corsOrigins                       []string
}

// mountPublic registra lo que pueden ver otros grupos: la capacidad publicada
// de Turnos (turnos-partner-v1) y la API de Notificaciones, ambas bajo /v1. La
// clave la envia el consumidor y la valida cada servicio.
func (c config) mountPublic(mux *http.ServeMux) {
	turnos := poolProxy(c.turnos, "")
	for _, path := range []string{"/v1/appointments", "/v1/appointments/", "/v1/doctors", "/v1/usage"} {
		mux.Handle(path, turnos)
	}
	mux.Handle("/v1/", proxy("notifications", c.notifications, ""))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"service": "gateway", "status": "ok", "portalAuthImplemented": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sendError(w, 404, "NOT_FOUND", "Ruta inexistente.")
	})
}

// routes es el ruteo de la red interna del portal: agrega a lo publico las
// rutas /api del portal, con autenticacion y limite de trafico, y las metricas.
// Solo el registro, el login y la consulta de medicos se atienden sin token.
func (c config) routes() *http.ServeMux {
	mux := http.NewServeMux()
	pacientes := proxy("pacientes", c.pacientes, "/api/pacientes")
	mux.Handle("POST /api/pacientes/auth/register", c.portal("pacientes", open, pacientes))
	mux.Handle("POST /api/pacientes/auth/login", c.portal("pacientes", open, pacientes))
	mux.HandleFunc("/api/pacientes/internal/", func(w http.ResponseWriter, r *http.Request) { sendError(w, 404, "NOT_FOUND", "Ruta inexistente.") })
	mux.Handle("/api/pacientes/", c.portal("pacientes", protected, pacientes))
	medicos := proxy("medicos", c.medicos, "/api/medicos")
	mux.Handle("GET /api/medicos/", c.portal("medicos", open, medicos))
	mux.Handle("/api/medicos/", c.portal("medicos", protected, medicos))
	mux.HandleFunc("/api/turnos/internal/", func(w http.ResponseWriter, r *http.Request) { sendError(w, 404, "NOT_FOUND", "Ruta inexistente.") })
	mux.Handle("/api/turnos/", c.portal("turnos", protected, poolProxy(c.turnos, "/api/turnos")))
	mux.Handle("/api/historia/", c.portal("historia", protected, proxy("historia", c.historia, "/api/historia")))
	mux.Handle("GET /metrics", obs.MetricsHandler())
	c.mountPublic(mux)
	return mux
}

// publicRoutes es el unico ruteo que se expone fuera de la red del portal
// (ADR-008): solo /v1/* y /healthz, sin /api ni /metrics.
func (c config) publicRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	c.mountPublic(mux)
	return mux
}

func (c config) internalHandler() http.Handler {
	return withCORS(c.corsOrigins, obs.Middleware("gateway", c.routes()))
}

func (c config) publicHandler() http.Handler { return obs.Middleware("gateway", c.publicRoutes()) }

func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
}

// turnosTargets reads TURNOS_URLS (comma-separated instances) and falls back
// to the single TURNOS_URL.
func turnosTargets(list, single string) []string {
	var out []string
	for _, u := range strings.Split(list, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	if len(out) > 0 {
		return out
	}
	if single != "" {
		return []string{single}
	}
	return []string{"http://127.0.0.1:8081"}
}

func fail(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func loadConfig() config {
	auth, err := newAuthenticator(os.Getenv("JWT_SECRET"))
	if err != nil {
		fail("invalid configuration", "error", err.Error())
	}
	rps, err := strconv.ParseFloat(env("GATEWAY_RATE_LIMIT_RPS", "10"), 64)
	if err != nil || rps <= 0 {
		fail("GATEWAY_RATE_LIMIT_RPS must be a positive number")
	}
	burst, err := strconv.Atoi(env("GATEWAY_RATE_LIMIT_BURST", "20"))
	if err != nil || burst < 1 {
		fail("GATEWAY_RATE_LIMIT_BURST must be a positive integer")
	}
	turnos, err := newPool("turnos", turnosTargets(os.Getenv("TURNOS_URLS"), os.Getenv("TURNOS_URL")))
	if err != nil {
		fail("invalid configuration", "error", err.Error())
	}
	return config{
		turnos:        turnos,
		notifications: env("NOTIFICATIONS_URL", "http://127.0.0.1:8085"),
		pacientes:     env("PACIENTES_URL", "http://127.0.0.1:8082"),
		medicos:       env("MEDICOS_URL", "http://127.0.0.1:8083"),
		historia:      env("HISTORIA_URL", "http://127.0.0.1:8084"),
		auth:          auth,
		limiter:       newTokenBucket(rps, burst),
		corsOrigins:   parseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS")),
	}
}

func main() {
	obs.NewLogger("gateway")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := loadConfig()
	shutdown, err := obs.InitTracing(ctx, "gateway")
	if err != nil {
		fail("tracing init failed", "error", err.Error())
	}
	defer shutdown(context.Background())
	go cfg.turnos.watch(ctx, healthEvery, healthTimeout)

	addr := env("HTTP_ADDR", "127.0.0.1:8080")
	servers := []*http.Server{newServer(addr, cfg.internalHandler())}
	// PUBLIC_HTTP_ADDR es el puerto que se publica a otros grupos; HTTP_ADDR
	// queda en la red interna del portal.
	if public := os.Getenv("PUBLIC_HTTP_ADDR"); public != "" {
		if public == addr {
			fail("PUBLIC_HTTP_ADDR must differ from HTTP_ADDR")
		}
		servers = append(servers, newServer(public, cfg.publicHandler()))
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, s := range servers {
			_ = s.Shutdown(sctx)
		}
	}()
	errs := make(chan error, len(servers))
	for _, s := range servers {
		slog.Info("gateway listening", "event", "service.started", "addr", s.Addr)
		go func(s *http.Server) { errs <- s.ListenAndServe() }(s)
	}
	for range servers {
		if err := <-errs; err != nil && err != http.ErrServerClosed {
			fail("http server failed", "error", err.Error())
		}
	}
}
