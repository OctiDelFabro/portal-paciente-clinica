//go:build integration

// Energy columns (ADR-009) against real MySQL. Requires Docker:
// go test -tags integration ./services/turnos/...
package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"portal-paciente-clinica/services/turnos/internal/adapters/directory"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// startDSN starts an empty MySQL and returns its DSN, without applying the schema.
func startDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcmysql.Run(ctx, "mysql:8.4", tcmysql.WithDatabase("turnos"), tcmysql.WithUsername("turnos"), tcmysql.WithPassword("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

// An appointments table created before the energy columns keeps its rows:
// Open adds the columns, booked portal rows become pending and partner rows
// not_applicable. Opening again (a second instance) is a no-op.
func TestEnergyMigrationKeepsExistingRowsValid(t *testing.T) {
	dsn := startDSN(t)
	ctx := context.Background()
	cfg, _ := mysql.ParseDSN(dsn)
	cfg.MultiStatements, cfg.ParseTime = true, true
	raw, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, schema); err != nil { // previous shape: no energy columns
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	for _, id := range []string{"00000000-0000-4000-b000-000000000001", "00000000-0000-4000-b000-000000000002"} {
		if _, err := raw.ExecContext(ctx, `INSERT INTO appointments (id, patient_id, doctor_id, start_at, end_at, status, version)
			VALUES (?, 'pat-001', 'doc-001', ?, ?, 'booked', 1)`, id, start, start.Add(30*time.Minute)); err != nil {
			t.Fatal(err)
		}
		start = start.Add(time.Hour)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO partner_bookings VALUES ('00000000-0000-4000-b000-000000000002', 'carcruds', 'k', ?, 2500000, 'ARS', ?)`,
		fmt.Sprintf("%064d", 0), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if _, err := Open(ctx, dsn); err != nil {
		t.Fatalf("second open must be a no-op: %v", err)
	}
	portal, err := store.Get(ctx, "00000000-0000-4000-b000-000000000001")
	if err != nil || portal.Energy.Status != domain.EnergyPending || portal.Status != domain.StatusBooked {
		t.Fatalf("portal row: %+v %v", portal, err)
	}
	partner, err := store.Get(ctx, "00000000-0000-4000-b000-000000000002")
	if err != nil || partner.Energy.Status != domain.EnergyNotApplicable {
		t.Fatalf("partner row: %+v %v", partner, err)
	}
	pending, err := store.PendingEnergy(ctx, time.Now().UTC(), 10)
	if err != nil || len(pending) != 1 || pending[0].ID != portal.ID {
		t.Fatalf("pending = %+v %v", pending, err)
	}
}

type flakyEstimator struct{ err error }

func (f *flakyEstimator) Estimate(context.Context, domain.Consumption) (app.EnergyEstimate, error) {
	if f.err != nil {
		return app.EnergyEstimate{}, f.err
	}
	return app.EnergyEstimate{MicroKWh: 381_000, AlgorithmVersion: "consumo-v1"}, nil
}

// With the provider down the booking is stored with pending energy; when it
// comes back the re-estimator writes the exact kWh and cost.
func TestBookingWithPendingEnergyIsReestimated(t *testing.T) {
	store := startStore(t)
	now := time.Now().UTC()
	est := &flakyEstimator{err: app.ErrEnergyUnavailable}
	svc := &app.Service{Store: store, Directory: directory.Seed{}, Now: func() time.Time { return now },
		NewID:  func() string { return fmt.Sprintf("00000000-0000-4000-c000-%012d", idSeq.Add(1)) },
		Energy: est, Tariff: domain.Tariff{BaseCents: 2_500_000, CentsPerKWh: 18_000}}
	ctx := context.Background()
	a, err := svc.BookForPatient(ctx, "jwt-patient-9", "doc-001", now.Add(72*time.Hour).Truncate(time.Hour))
	if err != nil || a.Energy.Status != domain.EnergyPending {
		t.Fatalf("booking: %+v %v", a, err)
	}
	if got, _ := store.Get(ctx, a.ID); got.Energy.Status != domain.EnergyPending {
		t.Fatalf("stored: %+v", got.Energy)
	}
	est.err = nil
	if n := svc.ReestimatePending(ctx, 10); n != 1 {
		t.Fatalf("reestimated %d", n)
	}
	got, err := store.Get(ctx, a.ID)
	want := domain.Energy{Status: domain.EnergyEstimated, MicroKWh: 381_000, CostCents: 6858, AlgorithmVersion: "consumo-v1"}
	if err != nil || got.Energy != want || got.Version != 1 {
		t.Fatalf("got %+v %v", got, err)
	}
	if pending, _ := store.PendingEnergy(ctx, now, 10); len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}
}
