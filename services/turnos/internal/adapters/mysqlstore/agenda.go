package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"time"
)

type agendaReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readAgenda(ctx context.Context, q agendaReader, id string) (domain.Agenda, error) {
	a := domain.Agenda{DoctorID: id}
	var minutes, open, close int
	var days []byte
	e := q.QueryRowContext(ctx, "SELECT duration_minutes,weekdays,open_minutes,close_minutes FROM doctor_agendas WHERE doctor_id=?", id).Scan(&minutes, &days, &open, &close)
	if errors.Is(e, sql.ErrNoRows) {
		return a, app.ErrNotFound
	}
	if e != nil {
		return a, e
	}
	e = json.Unmarshal(days, &a.Schedule.Weekdays)
	a.Duration = time.Duration(minutes) * time.Minute
	a.Schedule.Open = time.Duration(open) * time.Minute
	a.Schedule.Close = time.Duration(close) * time.Minute
	return a, e
}
func (s *Store) SavedAgenda(ctx context.Context, id string) (domain.Agenda, error) {
	return readAgenda(ctx, s.DB, id)
}
func (t *mysqlTx) SavedAgenda(ctx context.Context, id string) (domain.Agenda, error) {
	return readAgenda(ctx, t.tx, id)
}
func (t *mysqlTx) SaveAgenda(ctx context.Context, a domain.Agenda) error {
	days, _ := json.Marshal(a.Schedule.Weekdays)
	_, e := t.tx.ExecContext(ctx, `INSERT INTO doctor_agendas(doctor_id,duration_minutes,weekdays,open_minutes,close_minutes) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE duration_minutes=VALUES(duration_minutes),weekdays=VALUES(weekdays),open_minutes=VALUES(open_minutes),close_minutes=VALUES(close_minutes)`, a.DoctorID, int(a.Duration/time.Minute), days, int(a.Schedule.Open/time.Minute), int(a.Schedule.Close/time.Minute))
	return e
}

type blockReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readBlocks(ctx context.Context, q blockReader, id string, from, to time.Time) ([]domain.Block, error) {
	rows, e := q.QueryContext(ctx, `SELECT id,doctor_id,start_at,end_at,reason FROM agenda_blocks WHERE doctor_id=? AND active=TRUE AND start_at<? AND end_at>? ORDER BY start_at,id`, id, to.UTC(), from.UTC())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Block{}
	for rows.Next() {
		var b domain.Block
		if e := rows.Scan(&b.ID, &b.DoctorID, &b.StartAt, &b.EndAt, &b.Reason); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) Blocks(ctx context.Context, id string, from, to time.Time) ([]domain.Block, error) {
	return readBlocks(ctx, s.DB, id, from, to)
}
func (t *mysqlTx) Blocks(ctx context.Context, id string, from, to time.Time) ([]domain.Block, error) {
	return readBlocks(ctx, t.tx, id, from, to)
}
func (t *mysqlTx) InsertBlock(ctx context.Context, b domain.Block) error {
	_, e := t.tx.ExecContext(ctx, `INSERT INTO agenda_blocks(id,doctor_id,start_at,end_at,reason) VALUES(?,?,?,?,?)`, b.ID, b.DoctorID, b.StartAt, b.EndAt, b.Reason)
	return e
}
func (t *mysqlTx) ReleaseBlock(ctx context.Context, id, block string) error {
	r, e := t.tx.ExecContext(ctx, `UPDATE agenda_blocks SET active=FALSE WHERE id=? AND doctor_id=? AND active=TRUE`, block, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}
