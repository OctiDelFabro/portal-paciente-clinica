//go:build integration

package messaging

import (
	"context"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"testing"
	"time"
)

// No consumer has run: the producer must prepare ALL durable subscriptions.
func TestProducerCreatesEverySubscriptionBeforePublishing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	broker, err := rabbitmq.Run(ctx, "rabbitmq:4.1-management")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Terminate(context.Background()) })
	url, err := broker.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err = DeclareTopology(ch); err != nil {
		t.Fatal(err)
	}
	if err = DeclareTopology(ch); err != nil {
		t.Fatal("topology must be idempotent:", err)
	}
	if err = ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	confirmations := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	err = ch.PublishWithContext(ctx, Exchange, RoutingBooked, true, false, amqp.Publishing{DeliveryMode: amqp.Persistent, Body: []byte(`{"eventId":"before-consumers"}`)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-confirmations:
		if !ack.Ack {
			t.Fatal("publish not confirmed")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, queue := range []string{"notifications.turnos.v1", "historia-clinica.turnos.v1", "medicos.indexer.v1"} {
		msg, ok, err := ch.Get(queue, false)
		if err != nil || !ok {
			t.Fatalf("subscription %s lost the event: ok=%v err=%v", queue, ok, err)
		}
		if string(msg.Body) != `{"eventId":"before-consumers"}` {
			t.Fatalf("unexpected delivery %q", msg.Body)
		}
		if err = msg.Ack(false); err != nil {
			t.Fatal(err)
		}
	}
}
