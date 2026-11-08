// Package messaging contiene la topologia RabbitMQ compartida (ADR-005) y la
// propagacion de trazas y correlacion a traves de los headers AMQP.
package messaging

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"

	"portal-paciente-clinica/internal/platform/obs"
)

const (
	Exchange      = "portal.events.v1"
	DeadExchange  = "portal.events.dlx"
	RoutingBooked = "turnos.reservado.v1"
	RoutingCancel = "turnos.cancelado.v1"
	correlationH  = "x-correlation-id"
)

// headerCarrier adapta amqp.Table a la interfaz de propagacion de OpenTelemetry.
type headerCarrier amqp.Table

func (c headerCarrier) Get(key string) string {
	v, _ := c[key].(string)
	return v
}
func (c headerCarrier) Set(key, value string) { c[key] = value }
func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectHeaders serializa el contexto de traza y la correlacion del ctx.
func InjectHeaders(ctx context.Context, headers amqp.Table) amqp.Table {
	if headers == nil {
		headers = amqp.Table{}
	}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier(headers))
	if id := obs.CorrelationID(ctx); id != "" {
		headers[correlationH] = id
	}
	return headers
}

// ExtractContext reconstruye el contexto remoto a partir de un mensaje consumido.
func ExtractContext(ctx context.Context, headers amqp.Table) context.Context {
	if headers == nil {
		return ctx
	}
	ctx = otel.GetTextMapPropagator().Extract(ctx, headerCarrier(headers))
	if id, ok := headers[correlationH].(string); ok && obs.ValidCorrelationID(id) {
		ctx = obs.WithCorrelationID(ctx, id)
	}
	return ctx
}

// DeclareExchanges crea el exchange topic durable de eventos y el de mensajes fallidos.
func DeclareExchanges(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(Exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare %s: %w", Exchange, err)
	}
	if err := ch.ExchangeDeclare(DeadExchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare %s: %w", DeadExchange, err)
	}
	return nil
}
