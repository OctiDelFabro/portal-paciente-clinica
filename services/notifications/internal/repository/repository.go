// Package repository es la capa de datos de Notificaciones (MySQL/InnoDB).
package repository

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound            = errors.New("not found")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different data")
)

type Notification struct {
	ID           string
	Consumer     string
	Source       string
	TemplateID   string
	Recipient    string
	Params       map[string]string
	Status       string
	Attempts     int
	TraceHeaders map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Notice struct {
	ID        string
	PatientID string
	TurnID    string
	Kind      string
}

type Repo struct{ DB *sql.DB }

func Open(ctx context.Context, dsn string) (*Repo, error) {
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
	return &Repo{DB: db}, nil
}

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// isRetryable cubre la clave duplicada de una solicitud concurrente y el
// deadlock o timeout de bloqueo, en los que InnoDB ya revirtio la transaccion.
func isRetryable(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == 1062 || me.Number == 1213 || me.Number == 1205)
}

func insertNotification(ctx context.Context, tx *sql.Tx, n Notification) error {
	params, _ := json.Marshal(n.Params)
	headers, _ := json.Marshal(n.TraceHeaders)
	_, err := tx.ExecContext(ctx, `INSERT INTO notifications
		(id, consumer, source, template_id, recipient, params, status, attempts, next_attempt_at, trace_headers, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		n.ID, n.Consumer, n.Source, n.TemplateID, n.Recipient, params, n.Status, n.CreatedAt, headers, n.CreatedAt, n.UpdatedAt)
	return err
}

// CreateIdempotent registra la solicitud antes de responder 202 (ADR-003). La
// clave primaria (consumer, idem_key) resuelve solicitudes concurrentes: la
// perdedora vuelve a leer y devuelve la respuesta original o un conflicto.
func (r *Repo) CreateIdempotent(ctx context.Context, key, fingerprint string, n Notification, ttl time.Duration) (Notification, bool, error) {
	for attempt := 0; attempt < 5; attempt++ {
		got, replayed, err := r.createOnce(ctx, key, fingerprint, n, ttl)
		if !isRetryable(err) {
			return got, replayed, err
		}
	}
	return Notification{}, false, errors.New("idempotency race not resolved")
}

// READ COMMITTED evita los gap locks: con REPEATABLE READ, dos SELECT FOR
// UPDATE sobre una clave inexistente bloquean el mismo hueco y los INSERT
// posteriores terminan en deadlock (error 1213, visto en el test de integracion).
func (r *Repo) createOnce(ctx context.Context, key, fingerprint string, n Notification, ttl time.Duration) (Notification, bool, error) {
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Notification{}, false, err
	}
	defer tx.Rollback()
	var prevFingerprint, prevID string
	var expires time.Time
	err = tx.QueryRowContext(ctx, `SELECT fingerprint, notification_id, expires_at FROM idempotency_keys
		WHERE consumer = ? AND idem_key = ? FOR UPDATE`, n.Consumer, key).Scan(&prevFingerprint, &prevID, &expires)
	switch {
	case err == nil && n.CreatedAt.Before(expires):
		if prevFingerprint != fingerprint {
			return Notification{}, false, ErrIdempotencyConflict
		}
		// Se responde con la aceptacion original, no con el estado actual.
		original, err := r.get(ctx, tx, prevID, n.Consumer)
		original.Status, original.UpdatedAt = "accepted", original.CreatedAt
		return original, true, err
	case err == nil:
		// La ventana de 24 h vencio: la clave puede identificar un nuevo envio.
		if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE consumer = ? AND idem_key = ?", n.Consumer, key); err != nil {
			return Notification{}, false, err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return Notification{}, false, err
	}
	if err := insertNotification(ctx, tx, n); err != nil {
		return Notification{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys (consumer, idem_key, fingerprint, notification_id, accepted_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, n.Consumer, key, fingerprint, n.ID, n.CreatedAt, n.CreatedAt.Add(ttl)); err != nil {
		return Notification{}, false, err
	}
	return n, false, tx.Commit()
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const columns = "id, consumer, source, template_id, recipient, params, status, attempts, trace_headers, created_at, updated_at"

func scan(row *sql.Row) (Notification, error) {
	var n Notification
	var params, headers []byte
	err := row.Scan(&n.ID, &n.Consumer, &n.Source, &n.TemplateID, &n.Recipient, &params, &n.Status, &n.Attempts, &headers, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	_ = json.Unmarshal(params, &n.Params)
	_ = json.Unmarshal(headers, &n.TraceHeaders)
	return n, err
}

func (r *Repo) get(ctx context.Context, q querier, id, consumer string) (Notification, error) {
	return scan(q.QueryRowContext(ctx, "SELECT "+columns+" FROM notifications WHERE id = ? AND consumer = ?", id, consumer))
}

// Get no revela solicitudes de otro consumidor: responde ErrNotFound.
func (r *Repo) Get(ctx context.Context, id, consumer string) (Notification, error) {
	return r.get(ctx, r.DB, id, consumer)
}

// RecordEvent guarda inbox, notificaciones y aviso del portal en una sola
// transaccion. Si el evento ya estaba en inbox devuelve duplicate=true sin
// crear trabajos nuevos (consumidor idempotente, ADR-005).
func (r *Repo) RecordEvent(ctx context.Context, eventID, eventType string, receivedAt time.Time, ns []Notification, notice Notice) (bool, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO inbox (event_id, event_type, received_at) VALUES (?, ?, ?)", eventID, eventType, receivedAt); err != nil {
		if isDuplicate(err) {
			return true, nil
		}
		return false, err
	}
	for _, n := range ns {
		if err := insertNotification(ctx, tx, n); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO portal_notices (id, patient_id, turn_id, kind, created_at) VALUES (?, ?, ?, ?, ?)",
		notice.ID, notice.PatientID, notice.TurnID, notice.Kind, receivedAt); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// ClaimDue reserva notificaciones pendientes por un plazo (lease) para que
// otra instancia no las envie al mismo tiempo.
func (r *Repo) ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Notification, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM notifications WHERE status = 'accepted' AND next_attempt_at <= ? ORDER BY next_attempt_at LIMIT ? FOR UPDATE SKIP LOCKED", now, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Notification
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "UPDATE notifications SET next_attempt_at = ? WHERE id = ?", now.Add(lease), id); err != nil {
			return nil, err
		}
		n, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM notifications WHERE id = ?", id))
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, tx.Commit()
}

func (r *Repo) MarkSent(ctx context.Context, id string, now time.Time) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE notifications SET status = 'sent', attempts = attempts + 1, updated_at = ? WHERE id = ?", now, id)
	return err
}

func (r *Repo) MarkRetry(ctx context.Context, id string, now, next time.Time) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE notifications SET attempts = attempts + 1, next_attempt_at = ?, updated_at = ? WHERE id = ?", next, now, id)
	return err
}

func (r *Repo) MarkFailed(ctx context.Context, id string, now time.Time) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE notifications SET status = 'failed', attempts = attempts + 1, updated_at = ? WHERE id = ?", now, id)
	return err
}

// Release devuelve una notificacion reservada sin contar un intento (por
// ejemplo, con el circuit breaker abierto).
func (r *Repo) Release(ctx context.Context, id string, next time.Time) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE notifications SET next_attempt_at = ? WHERE id = ?", next, id)
	return err
}

func (r *Repo) Ping(ctx context.Context) error { return r.DB.PingContext(ctx) }
