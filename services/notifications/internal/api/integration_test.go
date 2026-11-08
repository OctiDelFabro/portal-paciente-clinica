//go:build integration

// Tests de integracion contra MySQL y RabbitMQ reales (enunciado 2.11).
// Requiere Docker: go test -tags integration ./services/notifications/...
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/notifications/internal/contacts"
	"portal-paciente-clinica/services/notifications/internal/repository"
	"portal-paciente-clinica/services/notifications/internal/service"
)

func startRepo(t *testing.T) *repository.Repo {
	t.Helper()
	ctx := context.Background()
	c, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("notificaciones"), mysql.WithUsername("notif"), mysql.WithPassword("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, _ := c.ConnectionString(ctx)
	repo, err := repository.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func newService(repo *repository.Repo) *service.Service {
	return &service.Service{Repo: repo, Contacts: contacts.Seed{}, Now: time.Now, NewID: obs.NewID, TTL: 24 * time.Hour,
		Breaker: &service.Breaker{Threshold: 5, Cooldown: time.Second}}
}

// 30 POST concurrentes con la misma clave: una sola solicitud y un mismo ID,
// como probaba el mock, pero ahora con la restriccion de MySQL.
func TestConcurrentSameIdempotencyKeyCreatesOneNotification(t *testing.T) {
	repo := startRepo(t)
	mux := http.NewServeMux()
	(&HTTP{Service: newService(repo), Keys: map[string]string{"team-a": "demo-a"}, Rate: 1000, Health: func() error { return nil }}).Register(mux)
	var wg sync.WaitGroup
	ids := make([]string, 30)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := do(mux, "POST", "/v1/notifications", "demo-a", "reserva-123", "application/json", validBody)
			if rec.Code != 202 {
				t.Errorf("status %d %s", rec.Code, rec.Body)
			}
			ids[i] = rec.Header().Get("Location")
		}(i)
	}
	wg.Wait()
	var count int
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count)
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("different ids returned: %v", ids)
		}
	}
	if count != 1 {
		t.Fatalf("want 1 row, got %d", count)
	}
	rec := do(mux, "POST", "/v1/notifications", "demo-a", "reserva-123", "application/json", strings.Replace(validBody, "T-1", "T-2", 1))
	if rec.Code != 409 {
		t.Fatalf("changed body with same key: want 409, got %d", rec.Code)
	}
}

// Un evento repetido no crea trabajos nuevos y uno mal formado llega a la DLQ.
func TestConsumerIsIdempotentAndDeadLettersPoison(t *testing.T) {
	repo := startRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc, err := rabbitmq.Run(ctx, "rabbitmq:4.1-management")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Terminate(context.Background()) })
	url, _ := rc.AmqpURL(ctx)
	go (&Consumer{URL: url, Service: newService(repo)}).Run(ctx)

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

	event := `{"eventId":"0b8f7c3a-1111-4222-8333-444455556666","eventType":"turnos.reservado.v1","version":1,"occurredAt":"2026-10-20T12:00:00Z","correlationId":"c-1","data":{"turnId":"t-1","patientId":"pat-003","doctorId":"doc-001","startAt":"2026-10-25T10:00:00Z","endAt":"2026-10-25T10:30:00Z","appointmentVersion":1}}`
	for i := 0; i < 2; i++ {
		publish(t, ch, messaging.RoutingBooked, event)
	}
	publish(t, ch, messaging.RoutingBooked, `{"broken":`)

	waitFor(t, func() bool { n, err := queueDepth(conn, DeadQueue); return err == nil && n == 1 })
	var inbox, notifs, notices int
	waitFor(t, func() bool {
		_ = repo.DB.QueryRow("SELECT COUNT(*) FROM inbox").Scan(&inbox)
		return inbox == 1
	})
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&notifs)
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM portal_notices").Scan(&notices)
	// pat-003 es menor sin correo: un solo correo, al responsable.
	if notifs != 1 || notices != 1 {
		t.Fatalf("want 1 notification and 1 notice, got %d and %d", notifs, notices)
	}
}

func publish(t *testing.T, ch *amqp.Channel, key, body string) {
	t.Helper()
	if err := ch.Publish(messaging.Exchange, key, false, false, amqp.Publishing{ContentType: "application/json", Body: []byte(body), MessageId: fmt.Sprint(time.Now().UnixNano())}); err != nil {
		t.Fatal(err)
	}
}

// queueDepth usa un canal propio: un declare pasivo fallido cierra el canal.
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
