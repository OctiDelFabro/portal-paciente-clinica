//go:build integration

// End-to-end sync test: MySQL outbox -> RabbitMQ -> indexer -> OpenSearch.
// Requires Docker: go test -tags integration ./services/medicos/...
package indexer

import (
	"context"
	"fmt"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	tcopensearch "github.com/testcontainers/testcontainers-go/modules/opensearch"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/medicos/internal/adapters/mysqlstore"
	"portal-paciente-clinica/services/medicos/internal/adapters/opensearch"
	"portal-paciente-clinica/services/medicos/internal/adapters/outbox"
	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestProfileChangeAndBookingsReachSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mc, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("medicos"), mysql.WithUsername("medicos"), mysql.WithPassword("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(context.Background()) })
	dsn, _ := mc.ConnectionString(ctx)
	store, err := mysqlstore.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	oc, err := tcopensearch.Run(ctx, "opensearchproject/opensearch:3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oc.Terminate(context.Background()) })
	osURL, _ := oc.Address(ctx)
	rc, err := rabbitmq.Run(ctx, "rabbitmq:4.1-management")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Terminate(context.Background()) })
	amqpURL, _ := rc.AmqpURL(ctx)

	now := time.Now().UTC().Truncate(time.Second)
	var seq int
	svc := &app.Service{Store: store, Index: opensearch.New(osURL), Now: func() time.Time { return now },
		NewID: func() string { seq++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", seq) }}
	if _, _, err := svc.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	go (&outbox.Relay{DB: store.DB, URL: amqpURL, Interval: 200 * time.Millisecond}).Run(ctx)
	go (&Consumer{URL: amqpURL, Handler: svc, Now: time.Now}).Run(ctx)

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waitFor(t, "indexer queue", func() bool { _, err := depth(conn, Queue); return err == nil })

	search := func(q domain.Query) domain.Page {
		p, _ := svc.Search(ctx, q)
		return p
	}
	if _, err := svc.UpdateProfile(ctx, "doc-001", domain.Profile{FullName: "Laura Gómez", Specialties: []string{"dermatologia"}, Active: true}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	waitFor(t, "profile change in search", func() bool {
		return search(domain.Query{Specialty: "dermatologia", Page: 1, Size: 10}).Total == 1
	})
	if lag := time.Since(start); lag > 5*time.Second {
		t.Fatalf("index lag %s exceeds ADR-006 objective of 5 s", lag)
	}

	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	bookStart := now.Add(domain.MinLeadTime)
	booked := fmt.Sprintf(`{"eventId":"e-b1","eventType":"turnos.reservado.v1","version":1,"occurredAt":%q,"correlationId":"c","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":%q,"endAt":%q,"appointmentVersion":1}}`,
		now.Format(time.RFC3339), bookStart.Format(time.RFC3339), bookStart.Add(30*time.Minute).Format(time.RFC3339))
	for i := 0; i < 2; i++ { // redelivery must be harmless
		publish(t, ch, messaging.RoutingBooked, booked)
	}
	publish(t, ch, messaging.RoutingBooked, `{"broken":`)
	want := bookStart.Add(30 * time.Minute)
	waitFor(t, "nextAvailableAt after booking", func() bool {
		for _, d := range search(domain.Query{Page: 1, Size: 10}).Items {
			if d.DoctorID == "doc-001" && d.NextAvailableAt != nil && d.NextAvailableAt.Equal(want) {
				return true
			}
		}
		return false
	})
	waitFor(t, "poison message in DLQ", func() bool { n, err := depth(conn, DeadQueue); return err == nil && n == 1 })
}

func publish(t *testing.T, ch *amqp.Channel, key, body string) {
	t.Helper()
	if err := ch.Publish(messaging.Exchange, key, false, false, amqp.Publishing{ContentType: "application/json", Body: []byte(body), MessageId: fmt.Sprint(time.Now().UnixNano())}); err != nil {
		t.Fatal(err)
	}
}

// depth uses its own channel: a failed passive declare closes the channel.
func depth(conn *amqp.Connection, name string) (int, error) {
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	defer ch.Close()
	q, err := ch.QueueDeclarePassive(name, true, false, false, false, nil)
	return q.Messages, err
}
