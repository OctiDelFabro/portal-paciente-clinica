// Package outbox publica en RabbitMQ los eventos guardados en la tabla outbox
// (ADR-005). Si el broker no esta disponible, las reservas siguen aceptandose y
// los eventos esperan; las metricas muestran ese atraso.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/internal/platform/obs"
)

var (
	pending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_pending",
		Help: "Eventos registrados todavia no confirmados por RabbitMQ.",
	})
	oldestAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_oldest_age_seconds",
		Help: "Antiguedad del evento pendiente mas viejo; crece si el broker no esta disponible.",
	})
	published = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "outbox_publish_total",
		Help: "Intentos de publicacion, por resultado (ok, returned, error).",
	}, []string{"result"})
)

const (
	batchSize      = 50
	confirmTimeout = 5 * time.Second
)

type Relay struct {
	DB       *sql.DB
	URL      string
	Interval time.Duration

	conn    *amqp.Connection
	ch      *amqp.Channel
	returns chan amqp.Return
}

// Run publica periodicamente hasta que ctx se cancela.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		r.refreshGauges(ctx)
		if err := r.ensureChannel(); err != nil {
			slog.WarnContext(ctx, "broker unavailable, events stay pending", "event", "outbox.broker_unavailable", "error", err.Error())
		} else if err := r.publishBatch(ctx); err != nil {
			slog.WarnContext(ctx, "outbox batch failed", "event", "outbox.batch_failed", "error", err.Error())
			if !errors.Is(err, errReturned) {
				r.reset()
			}
		}
		select {
		case <-ctx.Done():
			r.reset()
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) refreshGauges(ctx context.Context) {
	var count int
	var age sql.NullFloat64
	err := r.DB.QueryRowContext(ctx, `SELECT COUNT(*), TIMESTAMPDIFF(MICROSECOND, MIN(created_at), UTC_TIMESTAMP(3)) / 1e6
		FROM outbox WHERE published_at IS NULL`).Scan(&count, &age)
	if err != nil {
		return
	}
	pending.Set(float64(count))
	oldestAge.Set(age.Float64)
}

func (r *Relay) ensureChannel() error {
	if r.ch != nil && !r.ch.IsClosed() {
		return nil
	}
	r.reset()
	conn, err := amqp.DialConfig(r.URL, amqp.Config{Dial: amqp.DefaultDial(3 * time.Second)})
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err == nil {
		err = ch.Confirm(false)
	}
	if err == nil {
		err = messaging.DeclareExchanges(ch)
	}
	if err != nil {
		_ = conn.Close()
		return err
	}
	r.conn, r.ch = conn, ch
	// Con mandatory, un evento sin cola enlazada vuelve por aqui antes del ack.
	r.returns = ch.NotifyReturn(make(chan amqp.Return, batchSize))
	return nil
}

func (r *Relay) reset() {
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.conn, r.ch = nil, nil
}

type row struct {
	id         int64
	eventID    string
	routingKey string
	payload    []byte
	headers    map[string]string
}

// publishBatch toma eventos con SKIP LOCKED para que varias instancias de
// Turnos no publiquen el mismo evento a la vez.
func (r *Relay) publishBatch(ctx context.Context) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, event_id, routing_key, payload, headers FROM outbox
		WHERE published_at IS NULL ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`, batchSize)
	if err != nil {
		return err
	}
	var batch []row
	for rows.Next() {
		var x row
		var headers []byte
		if err := rows.Scan(&x.id, &x.eventID, &x.routingKey, &x.payload, &headers); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(headers, &x.headers)
		batch = append(batch, x)
	}
	rows.Close()
	for _, x := range batch {
		if err := r.publish(ctx, x); err != nil {
			_, _ = tx.ExecContext(ctx, "UPDATE outbox SET attempts = attempts + 1 WHERE id = ?", x.id)
			_ = tx.Commit()
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE outbox SET published_at = UTC_TIMESTAMP(3), attempts = attempts + 1 WHERE id = ?", x.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var errReturned = errors.New("event returned by broker: no queue bound")

func (r *Relay) publish(ctx context.Context, x row) error {
	// Continua la traza de la solicitud que creo el evento.
	parent := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(x.headers))
	if id := x.headers["x-correlation-id"]; obs.ValidCorrelationID(id) {
		parent = obs.WithCorrelationID(parent, id)
	}
	pctx, span := otel.Tracer("turnos").Start(parent, "publish "+x.routingKey, trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	cctx, cancel := context.WithTimeout(pctx, confirmTimeout)
	defer cancel()
	conf, err := r.ch.PublishWithDeferredConfirmWithContext(cctx, messaging.Exchange, x.routingKey, true, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: x.eventID,
		Timestamp: time.Now().UTC(), Type: x.routingKey, Body: x.payload, Headers: messaging.InjectHeaders(pctx, nil),
	})
	if err != nil {
		published.WithLabelValues("error").Inc()
		span.RecordError(err)
		return err
	}
	ok, err := conf.WaitContext(cctx)
	if err != nil || !ok {
		published.WithLabelValues("error").Inc()
		if err == nil {
			err = errors.New("broker nack")
		}
		span.RecordError(err)
		return err
	}
	select {
	case ret := <-r.returns:
		if ret.MessageId == x.eventID {
			published.WithLabelValues("returned").Inc()
			slog.WarnContext(pctx, "event returned, kept pending", "event", "outbox.returned", "event_id", x.eventID)
			return errReturned
		}
	default:
	}
	published.WithLabelValues("ok").Inc()
	slog.InfoContext(pctx, "event published", "event", "outbox.published", "event_id", x.eventID, "routing_key", x.routingKey)
	return nil
}
