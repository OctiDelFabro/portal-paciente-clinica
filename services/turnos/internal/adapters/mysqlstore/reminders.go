package mysqlstore

import (
	"context"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"time"
)

func (s *Store) DueReminders(ctx context.Context, now time.Time, limit int) ([]domain.Appointment, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT `+columns+` FROM appointments WHERE status='booked' AND start_at>? AND start_at<=? AND NOT EXISTS(SELECT 1 FROM appointment_reminders r WHERE r.appointment_id=appointments.id) ORDER BY start_at,id LIMIT ?`, now.UTC(), now.Add(24*time.Hour).UTC(), limit)
	return scanAll(rows, e)
}
func (t *mysqlTx) MarkReminder(ctx context.Context, id string) (bool, error) {
	r, e := t.tx.ExecContext(ctx, `INSERT IGNORE INTO appointment_reminders(appointment_id) VALUES(?)`, id)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}
