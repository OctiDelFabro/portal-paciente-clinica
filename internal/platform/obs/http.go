package obs

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Metricas RED por ruta. route es el patron registrado, nunca la URL real,
// para que IDs de turnos o pacientes no generen series ilimitadas.
var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Solicitudes HTTP atendidas, por ruta y codigo de estado.",
	}, []string{"service", "route", "method", "status"})
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duracion de solicitudes HTTP; permite calcular p95/p99.",
		Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2, 5},
	}, []string{"service", "route", "method"})
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Middleware aplica, en orden: correlacion, span de servidor OpenTelemetry,
// metricas RED y un log de acceso por solicitud.
func Middleware(service string, mux *http.ServeMux) http.Handler {
	measured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		httpRequests.WithLabelValues(service, route, r.Method, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(service, route, r.Method).Observe(elapsed.Seconds())
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "http request", "event", "http.request", "route", route, "method", r.Method,
			"status", rec.status, "duration_ms", elapsed.Milliseconds())
	})
	traced := otelhttp.NewHandler(measured, service, otelhttp.WithFilter(func(r *http.Request) bool {
		return r.URL.Path != "/healthz" && r.URL.Path != "/metrics"
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(CorrelationHeader)
		if !ValidCorrelationID(id) {
			id = NewID()
		}
		w.Header().Set(CorrelationHeader, id)
		traced.ServeHTTP(w, r.WithContext(WithCorrelationID(r.Context(), id)))
	})
}

// MetricsHandler expone el registro de Prometheus.
func MetricsHandler() http.Handler { return promhttp.Handler() }

// Client devuelve un cliente HTTP que propaga traceparent y X-Correlation-ID.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(http.DefaultTransport)}
}

// Transport envuelve base con span de cliente, traceparent y X-Correlation-ID.
func Transport(base http.RoundTripper) http.RoundTripper {
	return correlationTransport{otelhttp.NewTransport(base)}
}

type correlationTransport struct{ next http.RoundTripper }

func (t correlationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if id := CorrelationID(r.Context()); id != "" {
		r = r.Clone(r.Context())
		r.Header.Set(CorrelationHeader, id)
	}
	return t.next.RoundTrip(r)
}
