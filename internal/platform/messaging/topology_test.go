package messaging

import (
	amqp "github.com/rabbitmq/amqp091-go"
	"testing"
)

type topologyRecorder struct {
	queues   map[string]bool
	bindings map[string]map[string]bool
}

func (t *topologyRecorder) ExchangeDeclare(string, string, bool, bool, bool, bool, amqp.Table) error {
	return nil
}
func (t *topologyRecorder) QueueDeclare(name string, _ bool, _ bool, _ bool, _ bool, _ amqp.Table) (amqp.Queue, error) {
	t.queues[name] = true
	return amqp.Queue{Name: name}, nil
}
func (t *topologyRecorder) QueueBind(name, key, exchange string, _ bool, _ amqp.Table) error {
	if t.bindings[name] == nil {
		t.bindings[name] = map[string]bool{}
	}
	t.bindings[name][key] = true
	return nil
}
func TestProducerInitializesEveryConsumerBeforePublication(t *testing.T) {
	r := &topologyRecorder{queues: map[string]bool{}, bindings: map[string]map[string]bool{}}
	if err := DeclareTopology(r); err != nil {
		t.Fatal(err)
	}
	for _, queue := range []string{"notifications.turnos.v1", "historia-clinica.turnos.v1", "medicos.indexer.v1"} {
		for _, key := range []string{RoutingBooked, RoutingCancel} {
			if !r.bindings[queue][key] {
				t.Fatalf("missing binding %s %s", queue, key)
			}
		}
		for _, suffix := range []string{".dlq", ".retry.5s", ".retry.30s", ".retry.120s"} {
			if !r.queues[queue+suffix] {
				t.Fatalf("missing queue %s", queue+suffix)
			}
		}
	}
}
