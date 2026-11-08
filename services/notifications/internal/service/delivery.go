package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/notifications/internal/repository"
)

var (
	sendDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "email_send_duration_seconds",
		Help:    "Duracion de cada envio al proveedor de correo; base del timeout (D10).",
		Buckets: []float64{.05, .1, .25, .5, 1, 2, 5},
	})
	sendErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "email_send_errors_total",
		Help: "Envios fallidos al proveedor de correo.",
	})
	sendResults = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "notifications_delivered_total",
		Help: "Resultado final o intermedio de cada intento: sent, retry, failed, skipped_open_circuit.",
	}, []string{"result"})
	circuitOpen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "email_circuit_open",
		Help: "1 si el circuit breaker del proveedor de correo esta abierto.",
	})
)

// Mailer es el acceso al proveedor de correo (capa de datos/integracion).
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// RetryDelays son las esperas entre intentos de correo; tras agotarlas el
// estado pasa a failed (4 intentos en total).
var RetryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}

// NextAttempt devuelve cuando reintentar tras el intento numero attempt
// (empezando en 0), o false si no quedan reintentos.
func NextAttempt(attempt int, now time.Time) (time.Time, bool) {
	if attempt >= len(RetryDelays) {
		return time.Time{}, false
	}
	return now.Add(RetryDelays[attempt]), true
}

// Breaker abre el circuito tras Threshold fallas consecutivas y deja pasar un
// intento de prueba despues de Cooldown. Evita saturar un proveedor caido y
// consumir intentos de los correos mientras dura la caida.
type Breaker struct {
	Threshold int
	Cooldown  time.Duration

	mu       sync.Mutex
	failures int
	openedAt time.Time
}

func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures < b.Threshold || !now.Before(b.openedAt.Add(b.Cooldown))
}

func (b *Breaker) Record(ok bool, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ok {
		b.failures = 0
		circuitOpen.Set(0)
		return
	}
	b.failures++
	if b.failures >= b.Threshold {
		b.openedAt = now
		circuitOpen.Set(1)
	}
}

const (
	sendTimeout = 5 * time.Second
	lease       = 60 * time.Second
)

// RunDelivery envia periodicamente los correos pendientes.
func (s *Service) RunDelivery(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		due, err := s.Repo.ClaimDue(ctx, s.Now().UTC(), lease, 20)
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "cannot claim notifications", "event", "delivery.claim_failed", "error", err.Error())
		}
		for _, n := range due {
			s.deliver(ctx, n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) deliver(ctx context.Context, n repository.Notification) {
	parent := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(n.TraceHeaders))
	if id := n.TraceHeaders["x-correlation-id"]; obs.ValidCorrelationID(id) {
		parent = obs.WithCorrelationID(parent, id)
	}
	ctx, span := otel.Tracer("notifications").Start(parent, "send email", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	now := s.Now().UTC()
	if !s.Breaker.Allow(now) {
		sendResults.WithLabelValues("skipped_open_circuit").Inc()
		_ = s.Repo.Release(ctx, n.ID, now.Add(s.Breaker.Cooldown))
		return
	}
	subject, body := Render(n.TemplateID, n.Params)
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	start := time.Now()
	err := s.Mailer.Send(sctx, n.Recipient, subject, body)
	cancel()
	sendDuration.Observe(time.Since(start).Seconds())
	s.Breaker.Record(err == nil, now)
	if err == nil {
		sendResults.WithLabelValues("sent").Inc()
		_ = s.Repo.MarkSent(ctx, n.ID, s.Now().UTC())
		slog.InfoContext(ctx, "notification sent", "event", "notification.sent", "notification_id", n.ID, "template_id", n.TemplateID)
		return
	}
	sendErrors.Inc()
	span.RecordError(err)
	if next, ok := NextAttempt(n.Attempts, now); ok {
		sendResults.WithLabelValues("retry").Inc()
		_ = s.Repo.MarkRetry(ctx, n.ID, now, next)
		slog.WarnContext(ctx, "email failed, will retry", "event", "notification.retry", "notification_id", n.ID, "attempt", n.Attempts+1, "error", err.Error())
		return
	}
	sendResults.WithLabelValues("failed").Inc()
	_ = s.Repo.MarkFailed(ctx, n.ID, now)
	slog.ErrorContext(ctx, "email failed permanently", "event", "notification.failed", "notification_id", n.ID, "attempts", n.Attempts+1)
}
