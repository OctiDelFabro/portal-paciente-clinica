// Package mysqlstore implements app.Store over MySQL/InnoDB (ADR-003).
package mysqlstore

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }

// Open connects, applies the schema and the idempotent seed. DATETIMEs are UTC.
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
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{DB: db}
	if err := s.seed(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("seed: %w", err)
	}
	return s, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const doctorCols = "id, full_name, accepts_minors, active, profile_version"

func loadDoctor(ctx context.Context, q querier, query string, args ...any) (domain.Doctor, error) {
	var d domain.Doctor
	err := q.QueryRowContext(ctx, query, args...).Scan(&d.ID, &d.FullName, &d.AcceptsMinors, &d.Active, &d.ProfileVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return d, app.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Specialties, err = specialtiesOf(ctx, q, d.ID)
	return d, err
}

func specialtiesOf(ctx context.Context, q querier, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT specialty_code FROM doctor_specialties WHERE doctor_id = ? ORDER BY position", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Doctor(ctx context.Context, id string) (domain.Doctor, error) {
	return loadDoctor(ctx, s.DB, "SELECT "+doctorCols+" FROM doctors WHERE id = ?", id)
}

func (s *Store) Doctors(ctx context.Context) ([]domain.Doctor, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+doctorCols+" FROM doctors ORDER BY id")
	if err != nil {
		return nil, err
	}
	var out []domain.Doctor
	for rows.Next() {
		var d domain.Doctor
		if err := rows.Scan(&d.ID, &d.FullName, &d.AcceptsMinors, &d.Active, &d.ProfileVersion); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Specialties, err = specialtiesOf(ctx, s.DB, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) Specialties(ctx context.Context) ([]domain.Specialty, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT code, name FROM specialties ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Specialty{}
	for rows.Next() {
		var sp domain.Specialty
		if err := rows.Scan(&sp.Code, &sp.Name); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ApplyBooking keeps the highest appointmentVersion per turn, so redelivered
// or out-of-order events change nothing. MySQL reports 0 affected rows when
// the stored row stays identical.
func (s *Store) ApplyBooking(ctx context.Context, b app.Booking) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO doctor_bookings (turn_id, doctor_id, start_at, end_at, active, appointment_version)
		VALUES (?, ?, ?, ?, ?, ?) AS n
		ON DUPLICATE KEY UPDATE
		  doctor_id = IF(n.appointment_version > doctor_bookings.appointment_version, n.doctor_id, doctor_bookings.doctor_id),
		  start_at = IF(n.appointment_version > doctor_bookings.appointment_version, n.start_at, doctor_bookings.start_at),
		  end_at = IF(n.appointment_version > doctor_bookings.appointment_version, n.end_at, doctor_bookings.end_at),
		  active = IF(n.appointment_version > doctor_bookings.appointment_version, n.active, doctor_bookings.active),
		  appointment_version = GREATEST(n.appointment_version, doctor_bookings.appointment_version)`,
		b.TurnID, b.DoctorID, b.Start.UTC(), b.End.UTC(), b.Active, b.Version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) BusyIntervals(ctx context.Context, doctorID string, from time.Time) ([]domain.Interval, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT start_at, end_at FROM doctor_bookings
		WHERE doctor_id = ? AND active AND end_at > ? ORDER BY start_at LIMIT 500`, doctorID, from.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Interval
	for rows.Next() {
		var iv domain.Interval
		if err := rows.Scan(&iv.Start, &iv.End); err != nil {
			return nil, err
		}
		out = append(out, iv)
	}
	return out, rows.Err()
}

func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&mysqlTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

type mysqlTx struct{ tx *sql.Tx }

func (t *mysqlTx) GetForUpdate(ctx context.Context, id string) (domain.Doctor, error) {
	return loadDoctor(ctx, t.tx, "SELECT "+doctorCols+" FROM doctors WHERE id = ? FOR UPDATE", id)
}

func (t *mysqlTx) SpecialtiesExist(ctx context.Context, codes []string) (bool, error) {
	if len(codes) == 0 {
		return true, nil
	}
	args := make([]any, len(codes))
	for i, c := range codes {
		args[i] = c
	}
	var n int
	err := t.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM specialties WHERE code IN (?"+strings.Repeat(", ?", len(codes)-1)+")", args...).Scan(&n)
	return n == len(codes), err
}

func (t *mysqlTx) SaveProfile(ctx context.Context, d domain.Doctor) error {
	if _, err := t.tx.ExecContext(ctx, "UPDATE doctors SET full_name = ?, accepts_minors = ?, active = ?, profile_version = ? WHERE id = ?",
		d.FullName, d.AcceptsMinors, d.Active, d.ProfileVersion, d.ID); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(ctx, "DELETE FROM doctor_specialties WHERE doctor_id = ?", d.ID); err != nil {
		return err
	}
	for i, c := range d.Specialties {
		if _, err := t.tx.ExecContext(ctx, "INSERT INTO doctor_specialties (doctor_id, specialty_code, position) VALUES (?, ?, ?)", d.ID, c, i); err != nil {
			return err
		}
	}
	return nil
}

func (t *mysqlTx) AppendOutbox(ctx context.Context, m app.OutboxMessage) error {
	headers, _ := json.Marshal(m.Headers)
	_, err := t.tx.ExecContext(ctx, "INSERT INTO outbox (event_id, routing_key, payload, headers) VALUES (?, ?, ?, ?)",
		m.EventID, m.RoutingKey, m.Payload, headers)
	return err
}
