// Gateway: punto de entrada unico. Direcciona, propaga correlacion y trazas y
// aplica los tiempos de espera de ADR-005. La autenticacion del portal se
// incorpora en la presentacion final; no expone API keys al frontend.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/internal/platform/obs"
)

const (
	requestBudget = 5 * time.Second
	upstreamLimit = 2 * time.Second
)

var upstreamErrors = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gateway_upstream_errors_total",
	Help: "Fallas al llamar a un servicio interno, por servicio y tipo (timeout, unavailable).",
}, []string{"upstream", "kind"})

var retries = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gateway_retries_total",
	Help: "Reintentos de GET ante 502/503/504 o error de red, por servicio.",
}, []string{"upstream"})

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

// retryTransport repite una sola vez los GET (idempotentes) ante error de red
// o 502/503/504, con espera de 100-300 ms y solo si queda presupuesto. Las
// escrituras nunca se reintentan: una respuesta ausente no prueba que fallaron.
type retryTransport struct {
	name string
	next http.RoundTripper
}

func (t retryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if r.Method != http.MethodGet || !retryable(resp, err) {
		return resp, err
	}
	wait := time.Duration(100+rand.IntN(200)) * time.Millisecond
	if deadline, ok := r.Context().Deadline(); ok && time.Until(deadline) < wait+upstreamLimit/2 {
		return resp, err
	}
	if resp != nil {
		resp.Body.Close()
	}
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-time.After(wait):
	}
	retries.WithLabelValues(t.name).Inc()
	return t.next.RoundTrip(r)
}

func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}
	return resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
}

func proxy(name, target, stripPrefix string) http.Handler {
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		slog.Error("invalid upstream URL", "upstream", name)
		os.Exit(1)
	}
	base := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext,
		ResponseHeaderTimeout: upstreamLimit,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   50,
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.Transport = retryTransport{name: name, next: obs.Transport(base)}
	// El middleware del gateway ya devuelve X-Correlation-ID; sin esto el proxy
	// copiaria tambien el del servicio y el header saldria duplicado.
	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del(obs.CorrelationHeader)
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		var ne net.Error
		if errors.Is(e, context.DeadlineExceeded) || (errors.As(e, &ne) && ne.Timeout()) {
			upstreamErrors.WithLabelValues(name, "timeout").Inc()
			slog.WarnContext(r.Context(), "upstream timeout", "event", "gateway.upstream_timeout", "upstream", name)
			sendError(w, 504, "UPSTREAM_TIMEOUT", "El servicio no respondio a tiempo; el resultado puede ser indeterminado.")
			return
		}
		upstreamErrors.WithLabelValues(name, "unavailable").Inc()
		slog.WarnContext(r.Context(), "upstream unavailable", "event", "gateway.upstream_unavailable", "upstream", name, "error", e.Error())
		sendError(w, 502, "UPSTREAM_UNAVAILABLE", "Servicio no disponible.")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestBudget)
		defer cancel()
		r = r.WithContext(ctx)
		if stripPrefix != "" {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, stripPrefix)
			r.URL.RawPath = ""
		}
		rp.ServeHTTP(w, r)
	})
}

// routes arma el ruteo publico. La capacidad publicada de Turnos
// (turnos-partner-v1) y la API de Notificaciones comparten /v1; la clave la
// envia el consumidor y la valida cada servicio.
func routes(turnosURL, notificationsURL string) *http.ServeMux {
	turnos := proxy("turnos", turnosURL, "")
	mux := http.NewServeMux()
	mux.Handle("/api/turnos/", proxy("turnos", turnosURL, "/api/turnos"))
	for _, path := range []string{"/v1/appointments", "/v1/appointments/", "/v1/doctors", "/v1/usage"} {
		mux.Handle(path, turnos)
	}
	mux.Handle("/v1/", proxy("notifications", notificationsURL, ""))
	mux.Handle("GET /metrics", obs.MetricsHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"service": "gateway", "status": "ok", "portalAuthImplemented": false})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sendError(w, 404, "NOT_FOUND", "Ruta inexistente.")
	})
	return mux
}

func main() {
	obs.NewLogger("gateway")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := obs.InitTracing(ctx, "gateway")
	if err != nil {
		slog.Error("tracing init failed", "error", err.Error())
		os.Exit(1)
	}
	defer shutdown(context.Background())

	mux := routes(env("TURNOS_URL", "http://127.0.0.1:8081"), env("NOTIFICATIONS_URL", "http://127.0.0.1:8085"))

	addr := env("HTTP_ADDR", "127.0.0.1:8080")
	server := &http.Server{Addr: addr, Handler: obs.Middleware("gateway", mux), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(sctx)
	}()
	slog.Info("gateway listening", "event", "service.started", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("http server failed", "error", err.Error())
		os.Exit(1)
	}
}
