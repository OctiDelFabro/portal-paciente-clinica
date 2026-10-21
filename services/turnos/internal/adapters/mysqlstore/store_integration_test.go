//go:build integration

// Test de integracion contra MySQL real (enunciado 2.11). Requiere Docker:
// go test -tags integration ./services/turnos/...
package mysqlstore

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mysql"

	"portal-paciente-clinica/services/turnos/internal/adapters/directory"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

func startStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	c, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("turnos"), mysql.WithUsername("turnos"), mysql.WithPassword("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// Reservas concurrentes del mismo medico con horarios que se pisan parcialmente:
// el bloqueo por recurso debe dejar exactamente una (D4).
func TestConcurrentOverlappingBookingsKeepOne(t *testing.T) {
	store := startStore(t)
	now := time.Now().UTC()
	base := now.Add(72 * time.Hour).Truncate(time.Hour)
	var seq atomic.Int64
	svc := &app.Service{Store: store, Directory: directory.Seed{}, Now: func() time.Time { return now },
		NewID: func() string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", seq.Add(1)) }}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			patient := []string{"pat-001", "pat-003"}[i%2]
			start := base.Add(time.Duration(i%3) * 10 * time.Minute)
			if _, err := svc.Book(context.Background(), patient, "doc-001", start); err == nil {
				ok.Add(1)
			}
		}(i)
	}
	wg.Wait()
	var rows, events int
	_ = store.DB.QueryRow("SELECT COUNT(*) FROM appointments WHERE status = 'booked'").Scan(&rows)
	_ = store.DB.QueryRow("SELECT COUNT(*) FROM outbox").Scan(&events)
	if ok.Load() != 1 || rows != 1 || events != 1 {
		t.Fatalf("want one booking and one event, got ok=%d rows=%d events=%d", ok.Load(), rows, events)
	}
}

var idSeq atomic.Int64

func TestCancelPersistsVersionAndEvent(t *testing.T) {
	store := startStore(t)
	now := time.Now().UTC()
	svc := &app.Service{Store: store, Directory: directory.Seed{}, Now: func() time.Time { return now },
		NewID: func() string { return fmt.Sprintf("00000000-0000-4000-9000-%012d", idSeq.Add(1)) }}
	a, err := svc.Book(context.Background(), "pat-001", "doc-002", now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(context.Background(), a.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), a.ID)
	if err != nil || got.Status != domain.StatusCancelled || got.Version != 2 || got.CancelReason != "patient_request" {
		t.Fatalf("unexpected row: %+v %v", got, err)
	}
}
