package app

import (
	"context"
	"log/slog"
	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"time"
)

type reminderStore interface {
	DueReminders(context.Context, time.Time, int) ([]domain.Appointment, error)
}
type reminderTx interface {
	MarkReminder(context.Context, string) (bool, error)
}

// Both Turnos replicas may run this job: the appointment row and unique marker
// make reminder/outbox creation atomic and at most once per reservation.
func (s *Service) QueueReminders(ctx context.Context, limit int) error {
	store, ok := s.Store.(reminderStore)
	if !ok {
		return nil
	}
	due, e := store.DueReminders(ctx, s.Now(), limit)
	if e != nil {
		return e
	}
	for _, candidate := range due {
		if e := s.Store.InTx(ctx, func(tx Tx) error {
			a, e := tx.GetForUpdate(ctx, candidate.ID)
			if e != nil {
				return e
			}
			now := s.Now()
			if a.Status != domain.StatusBooked || !a.StartAt.After(now) || a.StartAt.After(now.Add(24*time.Hour)) {
				return nil
			}
			t, ok := tx.(reminderTx)
			if !ok {
				return nil
			}
			fresh, e := t.MarkReminder(ctx, a.ID)
			if e != nil || !fresh {
				return e
			}
			return tx.AppendOutbox(ctx, s.event(ctx, messaging.RoutingReminder, a))
		}); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) RunReminders(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if e := s.QueueReminders(ctx, 200); e != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "cannot queue reminders", "event", "reminder.retry", "error", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
