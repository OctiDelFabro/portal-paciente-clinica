// Package mysqlstore implementa app.Store sobre MySQL/InnoDB (ADR-003).
package mysqlstore

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

//go:embed schema.sql
var schema string

// lockWait mide cuanto espera una reserva por el bloqueo de medico y paciente:
// es el limite de capacidad esperado ante picos de demanda (D4, D13).
var lockWait = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "booking_lock_wait_seconds",
	Help:    "Espera para obtener los bloqueos de recurso de una reserva.",
	Buckets: []float64{.001, .005, .01, .05, .1, .25, .5, 1, 2},
})

const maxDeadlockRetries = 3

type Store struct{ DB *sql.DB }

// Open conecta y aplica el esquema. Los DATETIME se manejan en UTC.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ParseTime, cfg.Loc, cfg.MultiStatements = true, time.UTC, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrateEnergy(ctx, db); err != nil {
		return nil, fmt.Errorf("migrate energy columns: %w", err)
	}
	return &Store{DB: db}, nil
}

func isDeadlock(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205)
}

// InTx ejecuta fn en una transaccion; ante deadlock o timeout de bloqueo la
// reintenta completa, porque InnoDB ya la revirtio.
func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	var err error
	for attempt := 0; attempt <= maxDeadlockRetries; attempt++ {
		err = s.once(ctx, fn)
		if !isDeadlock(err) {
			return err
		}
		slog.WarnContext(ctx, "transaction retried after deadlock", "event", "db.deadlock_retry", "attempt", attempt+1)
	}
	return err
}

func (s *Store) once(ctx context.Context, fn func(app.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	if err := fn(&mysqlTx{s: s, tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const columns = "id, patient_id, doctor_id, start_at, end_at, status, version, COALESCE(cancel_reason, ''), " + energyColumns

type scanner interface{ Scan(...any) error }

func scan(row scanner) (domain.Appointment, error) {
	var a domain.Appointment
	var status string
	var e energyRow
	err := row.Scan(&a.ID, &a.PatientID, &a.DoctorID, &a.StartAt, &a.EndAt, &status, &a.Version, &a.CancelReason, &e.kwh, &e.cost, &e.status, &e.version)
	if errors.Is(err, sql.ErrNoRows) {
		return a, app.ErrNotFound
	}
	a.Status = domain.Status(status)
	if err == nil {
		a.Energy, err = e.energy()
	}
	return a, err
}

func (s *Store) Get(ctx context.Context, id string) (domain.Appointment, error) {
	return scan(s.DB.QueryRowContext(ctx, "SELECT "+columns+" FROM appointments WHERE id = ?", id))
}

type mysqlTx struct {
	s  *Store
	tx *sql.Tx
}

// LockResources crea las filas de recurso fuera de la transaccion (evita el
// deadlock de dos INSERT concurrentes) y luego las bloquea en orden estable.
func (t *mysqlTx) LockResources(ctx context.Context, keys ...string) error {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	placeholders := strings.TrimSuffix(strings.Repeat("(?),", len(sorted)), ",")
	args := make([]any, len(sorted))
	for i, k := range sorted {
		args[i] = k
	}
	if _, err := t.s.DB.ExecContext(ctx, "INSERT IGNORE INTO booking_locks (resource_key) VALUES "+placeholders, args...); err != nil {
		return err
	}
	start := time.Now()
	defer func() { lockWait.Observe(time.Since(start).Seconds()) }()
	for _, k := range sorted {
		var got string
		if err := t.tx.QueryRowContext(ctx, "SELECT resource_key FROM booking_locks WHERE resource_key = ? FOR UPDATE", k).Scan(&got); err != nil {
			return err
		}
	}
	return nil
}

func (t *mysqlTx) FindActiveOverlapping(ctx context.Context, doctorID, patientID string, start, end time.Time) ([]domain.Appointment, error) {
	rows, err := t.tx.QueryContext(ctx, "SELECT "+columns+` FROM appointments
		WHERE status = 'booked' AND (doctor_id = ? OR patient_id = ?) AND start_at < ? AND end_at > ?`,
		doctorID, patientID, end.UTC(), start.UTC())
	return scanAll(rows, err)
}

func scanAll(rows *sql.Rows, err error) ([]domain.Appointment, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Appointment{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActiveForDoctor is the availability read: a plain consistent read, no locks,
// so it never waits behind bookings (ADR-007). Uses idx_doctor_time.
func (s *Store) ActiveForDoctor(ctx context.Context, doctorID string, from, to time.Time) ([]domain.Appointment, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+` FROM appointments
		WHERE doctor_id = ? AND status = 'booked' AND start_at < ? AND end_at > ? ORDER BY start_at`,
		doctorID, to.UTC(), from.UTC())
	return scanAll(rows, err)
}

// listLimit bounds "my appointments"; the portal has no pagination yet.
const listLimit = 100

// ListByPatient returns the most recent appointments of the patient (any status).
func (s *Store) ListByPatient(ctx context.Context, patientID string) ([]domain.Appointment, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+` FROM appointments
		WHERE patient_id = ? ORDER BY start_at DESC, id LIMIT ?`, patientID, listLimit)
	return scanAll(rows, err)
}

func (t *mysqlTx) GetForUpdate(ctx context.Context, id string) (domain.Appointment, error) {
	return scan(t.tx.QueryRowContext(ctx, "SELECT "+columns+" FROM appointments WHERE id = ? FOR UPDATE", id))
}

func (t *mysqlTx) Insert(ctx context.Context, a domain.Appointment) error {
	kwh, cost, version := energyArgs(a.Energy)
	_, err := t.tx.ExecContext(ctx, `INSERT INTO appointments (id, patient_id, doctor_id, start_at, end_at, status, version,
		energy_kwh, energy_cost_ars, energy_status, energy_algorithm_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.ID, a.PatientID, a.DoctorID, a.StartAt.UTC(), a.EndAt.UTC(), string(a.Status), a.Version,
		kwh, cost, energyStatus(a.Energy), version)
	return err
}

// Update usa la version anterior como control optimista adicional.
func (t *mysqlTx) Update(ctx context.Context, a domain.Appointment) error {
	res, err := t.tx.ExecContext(ctx, `UPDATE appointments SET status = ?, version = ?, cancel_reason = NULLIF(?, '')
		WHERE id = ? AND version = ?`, string(a.Status), a.Version, a.CancelReason, a.ID, a.Version-1)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("appointment %s changed concurrently", a.ID)
	}
	return nil
}

func (t *mysqlTx) AppendOutbox(ctx context.Context, m app.OutboxMessage) error {
	headers, _ := json.Marshal(m.Headers)
	_, err := t.tx.ExecContext(ctx, "INSERT INTO outbox (event_id, routing_key, payload, headers) VALUES (?, ?, ?, ?)",
		m.EventID, m.RoutingKey, m.Payload, headers)
	return err
}

const partnerColumns = "appointment_id, consumer, idem_key, fingerprint, price_cents, currency, booked_at"

func scanPartner(row scanner) (app.PartnerBooking, error) {
	var pb app.PartnerBooking
	err := row.Scan(&pb.AppointmentID, &pb.Consumer, &pb.IdempotencyKey, &pb.Fingerprint, &pb.PriceCents, &pb.Currency, &pb.BookedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return pb, app.ErrNotFound
	}
	return pb, err
}

func (s *Store) PartnerBookingByKey(ctx context.Context, consumer, key string) (app.PartnerBooking, error) {
	return scanPartner(s.DB.QueryRowContext(ctx, "SELECT "+partnerColumns+" FROM partner_bookings WHERE consumer = ? AND idem_key = ?", consumer, key))
}

func (s *Store) PartnerBookingOf(ctx context.Context, appointmentID string) (app.PartnerBooking, error) {
	return scanPartner(s.DB.QueryRowContext(ctx, "SELECT "+partnerColumns+" FROM partner_bookings WHERE appointment_id = ?", appointmentID))
}

func (s *Store) PartnerUsage(ctx context.Context, consumer string, from, to time.Time) ([]app.UsageItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT pb.appointment_id, pb.booked_at, pb.price_cents, pb.currency, a.status
		FROM partner_bookings pb JOIN appointments a ON a.id = pb.appointment_id
		WHERE pb.consumer = ? AND pb.booked_at >= ? AND pb.booked_at < ?
		ORDER BY pb.booked_at, pb.appointment_id`, consumer, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.UsageItem
	for rows.Next() {
		var it app.UsageItem
		var status string
		if err := rows.Scan(&it.AppointmentID, &it.BookedAt, &it.PriceCents, &it.Currency, &status); err != nil {
			return nil, err
		}
		it.Status = domain.Status(status)
		out = append(out, it)
	}
	return out, rows.Err()
}

// FindPartnerBooking se ejecuta despues de bloquear la clave: con READ
// COMMITTED ve la reserva que haya confirmado otra solicitud con esa clave.
func (t *mysqlTx) FindPartnerBooking(ctx context.Context, consumer, key string) (app.PartnerBooking, error) {
	return scanPartner(t.tx.QueryRowContext(ctx, "SELECT "+partnerColumns+" FROM partner_bookings WHERE consumer = ? AND idem_key = ?", consumer, key))
}

func (t *mysqlTx) InsertPartnerBooking(ctx context.Context, pb app.PartnerBooking) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO partner_bookings (`+partnerColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		pb.AppointmentID, pb.Consumer, pb.IdempotencyKey, pb.Fingerprint, pb.PriceCents, pb.Currency, pb.BookedAt.UTC())
	return err
}
