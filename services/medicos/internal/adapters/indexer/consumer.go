package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/medicos/internal/app"
)

const (
	Queue        = "medicos.indexer.v1"
	DeadQueue    = Queue + ".dlq"
	retryHeader  = "x-retry-count"
	prefetch     = 10
	confirmLimit = 5 * time.Second
)

// RoutingKeys are the events the indexer binds to.
var RoutingKeys = []string{app.RoutingProfileUpdated, messaging.RoutingBooked, messaging.RoutingCancel}

// Same increasing back-off as Notificaciones (ADR-005): each wait queue sends
// the message back to the main queue when its TTL expires.
var retryQueues = []struct {
	name string
	ttl  time.Duration
}{{Queue + ".retry.5s", 5 * time.Second}, {Queue + ".retry.30s", 30 * time.Second}, {Queue + ".retry.120s", 120 * time.Second}}

var (
	consumed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumed_total",
		Help: "Events processed by the search indexer, by result: ok, duplicate, retry, dlq.",
	}, []string{"result"})
	dlqDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "medicos_indexer_dlq_depth",
		Help: "Messages waiting in the indexer dead letter queue for inspection.",
	})
	// indexLag measures the ADR-006 objective (p95 <= 5 s): event occurredAt to
	// the moment the document is written.
	indexLag = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "search_index_lag_seconds",
		Help:    "Delay between an event's occurredAt and its indexing in OpenSearch.",
		Buckets: []float64{.1, .25, .5, 1, 2, 5, 10, 30, 60, 300},
	})
)

type Consumer struct {
	URL     string
	Handler Handler
	Now     func() time.Time
}

// Run consumes until ctx is cancelled, reconnecting when the broker drops.
func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := c.session(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "broker session ended, reconnecting", "event", "consumer.reconnect", "error", err.Error())
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// Declare creates the indexer queue topology: main queue, DLQ and retry queues.
func Declare(ch *amqp.Channel) error {
	if err := messaging.DeclareExchanges(ch); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(Queue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": messaging.DeadExchange, "x-dead-letter-routing-key": Queue,
	}); err != nil {
		return err
	}
	for _, key := range RoutingKeys {
		if err := ch.QueueBind(Queue, key, messaging.Exchange, false, nil); err != nil {
			return err
		}
	}
	if _, err := ch.QueueDeclare(DeadQueue, true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.QueueBind(DeadQueue, Queue, messaging.DeadExchange, false, nil); err != nil {
		return err
	}
	for _, rq := range retryQueues {
		if _, err := ch.QueueDeclare(rq.name, true, false, false, false, amqp.Table{
			"x-message-ttl": rq.ttl.Milliseconds(), "x-dead-letter-exchange": "", "x-dead-letter-routing-key": Queue,
		}); err != nil {
			return fmt.Errorf("declare %s: %w", rq.name, err)
		}
	}
	return nil
}

func (c *Consumer) session(ctx context.Context) error {
	conn, err := amqp.DialConfig(c.URL, amqp.Config{Dial: amqp.DefaultDial(3 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := Declare(ch); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.Consume(Queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	go c.watchDLQ(ctx, conn)
	slog.InfoContext(ctx, "consuming events", "event", "consumer.started", "queue", Queue)
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			c.handle(ctx, ch, d)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, ch *amqp.Channel, d amqp.Delivery) {
	parent := messaging.ExtractContext(ctx, d.Headers)
	mctx, span := otel.Tracer("medicos").Start(parent, "consume "+d.RoutingKey, trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()

	result, occurredAt, err := Process(mctx, c.Handler, d.RoutingKey, d.Body)
	switch {
	case errors.Is(err, ErrUnprocessable):
		span.RecordError(err)
		c.deadLetter(mctx, d, err.Error())
	case err != nil:
		span.RecordError(err)
		c.retry(mctx, ch, d, err)
	default:
		consumed.WithLabelValues(result).Inc()
		if result == "ok" && !occurredAt.IsZero() {
			indexLag.Observe(c.Now().Sub(occurredAt).Seconds())
		}
		slog.InfoContext(mctx, "event indexed", "event", "search.indexed", "event_id", d.MessageId, "routing_key", d.RoutingKey, "result", result)
		_ = d.Ack(false)
	}
}

func retryCount(h amqp.Table) int {
	switch v := h[retryHeader].(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// retry publishes a copy to the matching wait queue and only acknowledges the
// original after the broker confirms it, so the event is never lost.
func (c *Consumer) retry(ctx context.Context, ch *amqp.Channel, d amqp.Delivery, cause error) {
	n := retryCount(d.Headers)
	if n >= len(retryQueues) {
		c.deadLetter(ctx, d, "retries_exhausted")
		return
	}
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers[retryHeader] = int32(n + 1)
	pctx, cancel := context.WithTimeout(ctx, confirmLimit)
	defer cancel()
	conf, err := ch.PublishWithDeferredConfirmWithContext(pctx, "", retryQueues[n].name, false, false, amqp.Publishing{
		ContentType: d.ContentType, DeliveryMode: amqp.Persistent, MessageId: d.MessageId, Type: d.Type, Body: d.Body, Headers: headers,
	})
	if err == nil {
		var ok bool
		ok, err = conf.WaitContext(pctx)
		if err == nil && !ok {
			err = errors.New("broker nack")
		}
	}
	if err != nil {
		// Without confirmation the message is requeued: indexing is idempotent.
		_ = d.Nack(false, true)
		return
	}
	consumed.WithLabelValues("retry").Inc()
	slog.WarnContext(ctx, "indexing failed, scheduled retry", "event", "event.retry", "event_id", d.MessageId,
		"retry", n+1, "delay", retryQueues[n].ttl.String(), "error", cause.Error())
	_ = d.Ack(false)
}

func (c *Consumer) deadLetter(ctx context.Context, d amqp.Delivery, reason string) {
	consumed.WithLabelValues("dlq").Inc()
	slog.ErrorContext(ctx, "event sent to dead letter queue", "event", "message.dead_lettered", "event_id", d.MessageId, "reason", reason)
	_ = d.Nack(false, false)
}

func (c *Consumer) watchDLQ(ctx context.Context, conn *amqp.Connection) {
	ch, err := conn.Channel()
	if err != nil {
		return
	}
	defer ch.Close()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if q, err := ch.QueueDeclarePassive(DeadQueue, true, false, false, false, nil); err == nil {
			dlqDepth.Set(float64(q.Messages))
		} else {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
