//go:build integration

// Consumer integration test against real MongoDB and RabbitMQ (brief 2.11).
// Requires Docker: go test -tags integration ./services/historia-clinica/...
package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"go.mongodb.org/mongo-driver/v2/bson"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/historia-clinica/internal/repository"
	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

const cancelBody = `{"eventId":"0b8f7c3a-2222-4222-8333-444455556666","eventType":"turnos.cancelado.v1","version":1,"occurredAt":"2026-10-21T12:00:00Z","correlationId":"c-2","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-10-25T10:00:00Z","endAt":"2026-10-25T10:30:00Z","appointmentVersion":2,"reasonCode":"patient_request"}}`

// A redelivered event changes nothing and unprocessable messages reach the DLQ.
func TestConsumerIsIdempotentAndDeadLettersUnprocessable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mc, err := mongodb.Run(ctx, "mongo:8.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	uri, _ := mc.ConnectionString(ctx)
	repo, err := repository.Open(ctx, uri, "historia_test")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := rabbitmq.Run(ctx, "rabbitmq:4.1-management")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Terminate(context.Background()) })
	url, _ := rc.AmqpURL(ctx)
	svc := &service.Service{Access: service.AccessFunc(func(context.Context, string, string, string) error { return nil }), Store: repo, Now: time.Now, NewID: obs.NewID}
	go (&Consumer{URL: url, Service: svc}).Run(ctx)

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waitFor(t, func() bool { _, err := queueDepth(conn, Queue); return err == nil })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}

	publish(t, ch, messaging.RoutingBooked, bookedBody)
	publish(t, ch, messaging.RoutingCancel, cancelBody)
	waitFor(t, func() bool { return count(t, repo, repository.ProcessedEvents) == 2 })
	before := encounter(t, repo, "t-1")

	// Redeliver both events; a later sentinel proves they were consumed (single ordered consumer).
	publish(t, ch, messaging.RoutingCancel, cancelBody)
	publish(t, ch, messaging.RoutingBooked, bookedBody)
	publish(t, ch, messaging.RoutingBooked, strings.NewReplacer("0b8f7c3a-1111", "0b8f7c3a-3333", `"turnId":"t-1"`, `"turnId":"t-9"`).Replace(bookedBody))
	waitFor(t, func() bool { return count(t, repo, repository.ProcessedEvents) == 3 })
	if after := encounter(t, repo, "t-1"); string(mustMarshal(t, before)) != string(mustMarshal(t, after)) {
		t.Fatalf("redelivery changed the encounter:\nbefore %v\nafter  %v", before, after)
	}
	got, _ := svc.Timeline(ctx, "pat-001")
	status := map[string]string{}
	for _, e := range got {
		status[e.AppointmentID] = e.Status
	}
	if len(got) != 2 || status["t-1"] != service.StatusCancelled || status["t-9"] != service.StatusScheduled {
		t.Fatalf("want t-1 cancelled and t-9 scheduled, got %+v", got)
	}

	publish(t, ch, messaging.RoutingBooked, `{"broken":`)
	publish(t, ch, messaging.RoutingBooked, strings.Replace(bookedBody, `"patientId":"pat-001"`, `"patientId":""`, 1))
	waitFor(t, func() bool { n, err := queueDepth(conn, DeadQueue); return err == nil && n == 2 })
	if n := count(t, repo, repository.ProcessedEvents); n != 3 {
		t.Fatalf("unprocessable messages reached storage: %d processed events", n)
	}
}

func count(t *testing.T, repo *repository.Repo, collection string) int64 {
	t.Helper()
	n, err := repo.DB.Collection(collection).CountDocuments(context.Background(), bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func encounter(t *testing.T, repo *repository.Repo, id string) bson.D {
	t.Helper()
	var doc bson.D
	if err := repo.DB.Collection(repository.Encounters).FindOne(context.Background(), bson.M{"_id": id}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func mustMarshal(t *testing.T, d bson.D) []byte {
	t.Helper()
	b, err := bson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func publish(t *testing.T, ch *amqp.Channel, key, body string) {
	t.Helper()
	if err := ch.Publish(messaging.Exchange, key, false, false, amqp.Publishing{ContentType: "application/json", Body: []byte(body), MessageId: fmt.Sprint(time.Now().UnixNano())}); err != nil {
		t.Fatal(err)
	}
}

// queueDepth uses its own channel: a failed passive declare closes the channel.
func queueDepth(conn *amqp.Connection, name string) (int, error) {
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	defer ch.Close()
	q, err := ch.QueueDeclarePassive(name, true, false, false, false, nil)
	return q.Messages, err
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("condition not met within 30s")
}
