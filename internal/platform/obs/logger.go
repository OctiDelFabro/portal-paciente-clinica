// Package obs concentra logs estructurados, correlacion, trazas y metricas
// compartidos por todos los servicios. Los campos y su justificacion estan en
// docs/adr/ADR-011-Observabilidad.md.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"regexp"

	"go.opentelemetry.io/otel/trace"
)

type correlationKey struct{}

// CorrelationHeader identifica una operacion de negocio entre servicios y mensajes.
const CorrelationHeader = "X-Correlation-ID"

var safeID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// NewID genera un UUID v4.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// ValidCorrelationID evita propagar valores arbitrarios recibidos del exterior.
func ValidCorrelationID(id string) bool { return safeID.MatchString(id) }

func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// contextHandler agrega correlation_id, trace_id y span_id a cada registro
// emitido con un contexto, para unir logs de distintos servicios y saltar a la traza.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := CorrelationID(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// NewLogger crea el logger JSON del servicio y lo instala como predeterminado.
// instance distingue replicas del mismo servicio (D12).
func NewLogger(service string) *slog.Logger {
	instance, _ := os.Hostname()
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(contextHandler{base}).With("service", service, "instance", instance)
	slog.SetDefault(logger)
	return logger
}
