package repository

import (
	"context"
	"database/sql"
	"time"
)

func insertNotice(ctx context.Context, tx *sql.Tx, n Notice, at time.Time) error {
	if n.ID == "" || n.PatientID == "" {
		return nil
	}
	if _, e := tx.ExecContext(ctx, `INSERT IGNORE INTO portal_notices(id,patient_id,turn_id,kind,created_at) VALUES(?,?,?,?,?)`, n.ID, n.PatientID, n.TurnID, n.Kind, at); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT IGNORE INTO portal_notice_details(notice_id,doctor_id,start_at,reason) VALUES(?,?,?,?)`, n.ID, n.DoctorID, n.StartAt, n.Reason)
	return e
}
func (r *Repo) EnsureNotice(ctx context.Context, n Notice, at time.Time) error {
	tx, e := r.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e := insertNotice(ctx, tx, n, at); e != nil {
		return e
	}
	return tx.Commit()
}
func (r *Repo) Notices(ctx context.Context, patient string, page int) ([]Notice, bool, error) {
	rows, e := r.DB.QueryContext(ctx, `SELECT n.id,n.patient_id,n.turn_id,n.kind,n.created_at,n.read_at,COALESCE(d.doctor_id,''),d.start_at,COALESCE(d.reason,'') FROM portal_notices n LEFT JOIN portal_notice_details d ON d.notice_id=n.id WHERE n.patient_id=? ORDER BY n.created_at DESC,n.id LIMIT 51 OFFSET ?`, patient, (page-1)*50)
	if e != nil {
		return nil, false, e
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		var start, read sql.NullTime
		if e := rows.Scan(&n.ID, &n.PatientID, &n.TurnID, &n.Kind, &n.CreatedAt, &read, &n.DoctorID, &start, &n.Reason); e != nil {
			return nil, false, e
		}
		if start.Valid {
			n.StartAt = &start.Time
		}
		if read.Valid {
			n.ReadAt = &read.Time
		}
		out = append(out, n)
	}
	if e := rows.Err(); e != nil {
		return nil, false, e
	}
	more := len(out) > 50
	if more {
		out = out[:50]
	}
	return out, more, nil
}
func (r *Repo) ReadNotice(ctx context.Context, id, patient string, at time.Time) error {
	res, e := r.DB.ExecContext(ctx, `UPDATE portal_notices SET read_at=COALESCE(read_at,?) WHERE id=? AND patient_id=?`, at, id, patient)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists bool
		if e := r.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM portal_notices WHERE id=? AND patient_id=?)`, id, patient).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
func (r *Repo) CancelDelivery(ctx context.Context, id string, now time.Time) error {
	_, e := r.DB.ExecContext(ctx, `UPDATE notifications SET status='cancelled',updated_at=? WHERE id=? AND status='accepted'`, now, id)
	return e
}
