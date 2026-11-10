package messaging

import (
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
)

// TopologyChannel allows the complete routing graph to be checked without a
// running broker. Producers initialize it before publishing any outbox entry.
type TopologyChannel interface {
	ExchangeDeclare(string, string, bool, bool, bool, bool, amqp.Table) error
	QueueDeclare(string, bool, bool, bool, bool, amqp.Table) (amqp.Queue, error)
	QueueBind(string, string, string, bool, amqp.Table) error
}

func DeclareTopology(ch TopologyChannel) error {
	if err := DeclareExchanges(ch); err != nil {
		return err
	}
	queues := []struct {
		name string
		keys []string
	}{
		{"notifications.turnos.v1", []string{RoutingBooked, RoutingCancel}},
		{"historia-clinica.turnos.v1", []string{RoutingBooked, RoutingCancel}},
		{"medicos.indexer.v1", []string{RoutingBooked, RoutingCancel, "medicos.perfil-actualizado.v1"}},
	}
	for _, q := range queues {
		if _, err := ch.QueueDeclare(q.name, true, false, false, false, amqp.Table{"x-dead-letter-exchange": DeadExchange, "x-dead-letter-routing-key": q.name}); err != nil {
			return fmt.Errorf("declare %s: %w", q.name, err)
		}
		for _, key := range q.keys {
			if err := ch.QueueBind(q.name, key, Exchange, false, nil); err != nil {
				return err
			}
		}
		if _, err := ch.QueueDeclare(q.name+".dlq", true, false, false, false, nil); err != nil {
			return err
		}
		if err := ch.QueueBind(q.name+".dlq", q.name, DeadExchange, false, nil); err != nil {
			return err
		}
		for _, delay := range []int64{5, 30, 120} {
			name := fmt.Sprintf("%s.retry.%ds", q.name, delay)
			if _, err := ch.QueueDeclare(name, true, false, false, false, amqp.Table{"x-message-ttl": delay * 1000, "x-dead-letter-exchange": "", "x-dead-letter-routing-key": q.name}); err != nil {
				return err
			}
		}
	}
	return nil
}
