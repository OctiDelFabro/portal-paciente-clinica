package app

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type AgendaStore interface {
	SavedAgenda(context.Context, string) (domain.Agenda, error)
	Blocks(context.Context, string, time.Time, time.Time) ([]domain.Block, error)
}
type AgendaTx interface {
	Tx
	SavedAgenda(context.Context, string) (domain.Agenda, error)
	SaveAgenda(context.Context, domain.Agenda) error
	Blocks(context.Context, string, time.Time, time.Time) ([]domain.Block, error)
	InsertBlock(context.Context, domain.Block) error
	ReleaseBlock(context.Context, string, string) error
}

func applyAgenda(d domain.Doctor, a domain.Agenda) domain.Doctor {
	d.Duration, d.Schedule = a.Duration, a.Schedule
	return d
}
func (s *Service) doctor(ctx context.Context, id string) (domain.Doctor, error) {
	d, err := s.Directory.Doctor(ctx, id)
	if err != nil {
		return d, err
	}
	if store, ok := s.Store.(AgendaStore); ok {
		a, e := store.SavedAgenda(ctx, id)
		if e == nil {
			d = applyAgenda(d, a)
		} else if !errors.Is(e, ErrNotFound) {
			return d, e
		}
	}
	return d, nil
}
func (s *Service) agendaInTx(ctx context.Context, tx Tx, d domain.Doctor, start, end time.Time, existing []domain.Appointment) (domain.Doctor, []domain.Appointment, error) {
	if t, ok := tx.(AgendaTx); ok {
		a, e := t.SavedAgenda(ctx, d.ID)
		if e == nil {
			d = applyAgenda(d, a)
		} else if !errors.Is(e, ErrNotFound) {
			return d, nil, e
		}
		end = start.Add(d.Duration)
		blocks, e := t.Blocks(ctx, d.ID, start, end)
		if e != nil {
			return d, nil, e
		}
		existing = domain.WithBlocks(existing, blocks)
	}
	return d, existing, nil
}
func (s *Service) blocks(ctx context.Context, id string, start, end time.Time) ([]domain.Block, error) {
	if store, ok := s.Store.(AgendaStore); ok {
		return store.Blocks(ctx, id, start, end)
	}
	return nil, nil
}
func (s *Service) Schedule(ctx context.Context, id string) (domain.Agenda, error) {
	d, e := s.doctor(ctx, id)
	return domain.Agenda{DoctorID: id, Duration: d.Duration, Schedule: d.Schedule}, e
}
func (s *Service) SetSchedule(ctx context.Context, id string, a domain.Agenda) error {
	a.DoctorID = id
	if e := a.Validate(); e != nil {
		return e
	}
	if _, e := s.Directory.Doctor(ctx, id); e != nil {
		return e
	}
	e := s.Store.InTx(ctx, func(tx Tx) error {
		if e := tx.LockResources(ctx, DoctorKey(id)); e != nil {
			return e
		}
		t, ok := tx.(AgendaTx)
		if !ok {
			return domain.ErrInvalidAgenda
		}
		// Existing reservations retain their own duration, including after an edit.
		current, e := tx.FindActiveOverlapping(ctx, id, "", s.Now(), s.Now().AddDate(100, 0, 0))
		if e != nil {
			return e
		}
		for _, v := range current {
			if v.DoctorID == id && !a.Schedule.Allows(v.StartAt, v.EndAt.Sub(v.StartAt)) {
				return domain.ErrAgendaConflict
			}
		}
		return t.SaveAgenda(ctx, a)
	})
	if e == nil {
		s.invalidateRange(ctx, id, s.Now(), s.Now().AddDate(0, 0, NextAvailableHorizonDays))
	}
	return e
}
func (s *Service) ListBlocks(ctx context.Context, id string) ([]domain.Block, error) {
	return s.blocks(ctx, id, s.Now(), s.Now().AddDate(1, 0, 0))
}
func (s *Service) BlockAgenda(ctx context.Context, id string, start, end time.Time, reason string) (domain.Block, error) {
	b := domain.Block{ID: s.NewID(), DoctorID: id, StartAt: start.UTC(), EndAt: end.UTC(), Reason: strings.TrimSpace(reason)}
	if start.Before(s.Now()) || !end.After(start) || end.Sub(start) > 366*24*time.Hour || utf8.RuneCountInString(b.Reason) > 200 || strings.ContainsAny(b.Reason, "\r\n\x00") {
		return b, domain.ErrInvalidAgenda
	}
	if b.Reason == "" {
		b.Reason = "Bloqueo de agenda del profesional"
	}
	if _, e := s.Directory.Doctor(ctx, id); e != nil {
		return b, e
	}
	var cancelled []domain.Appointment
	e := s.Store.InTx(ctx, func(tx Tx) error {
		cancelled = nil
		if e := tx.LockResources(ctx, DoctorKey(id)); e != nil {
			return e
		}
		t, ok := tx.(AgendaTx)
		if !ok {
			return domain.ErrInvalidAgenda
		}
		list, e := tx.FindActiveOverlapping(ctx, id, "", start, end)
		if e != nil {
			return e
		}
		keys := []string{}
		for _, a := range list {
			if a.DoctorID == id {
				keys = append(keys, PatientKey(a.PatientID))
			}
		}
		if e := tx.LockResources(ctx, keys...); e != nil {
			return e
		}
		if e := t.InsertBlock(ctx, b); e != nil {
			return e
		}
		for _, old := range list {
			if old.DoctorID != id {
				continue
			}
			a, e := tx.GetForUpdate(ctx, old.ID)
			if e != nil {
				return e
			}
			if a.Status != domain.StatusBooked {
				continue
			}
			a.Status = domain.StatusCancelled
			a.CancelReason = b.Reason
			a.Version++
			if e := tx.Update(ctx, a); e != nil {
				return e
			}
			if e := tx.AppendOutbox(ctx, s.event(ctx, messaging.RoutingCancel, a)); e != nil {
				return e
			}
			cancelled = append(cancelled, a)
		}
		return nil
	})
	if e == nil {
		s.invalidateRange(ctx, id, start, end)
		for _, a := range cancelled {
			s.invalidate(ctx, a)
		}
	}
	return b, e
}
func (s *Service) ReleaseBlock(ctx context.Context, id, block string) error {
	e := s.Store.InTx(ctx, func(tx Tx) error {
		if e := tx.LockResources(ctx, DoctorKey(id)); e != nil {
			return e
		}
		t, ok := tx.(AgendaTx)
		if !ok {
			return domain.ErrInvalidAgenda
		}
		return t.ReleaseBlock(ctx, id, block)
	})
	if e == nil {
		s.invalidateRange(ctx, id, s.Now(), s.Now().AddDate(1, 0, 0))
	}
	return e
}
func (s *Service) invalidateRange(ctx context.Context, id string, start, end time.Time) {
	if s.Cache == nil {
		return
	}
	day, _ := domain.ParseDay(domain.DayOf(start))
	if cache, ok := s.Cache.(interface {
		InvalidateDays(context.Context, string, []string)
	}); ok {
		days := []string{}
		for ; day.Before(end); day = day.AddDate(0, 0, 1) {
			days = append(days, domain.DayOf(day))
		}
		cache.InvalidateDays(ctx, id, days)
		return
	}
	for ; day.Before(end); day = day.AddDate(0, 0, 1) {
		s.Cache.Invalidate(ctx, id, domain.DayOf(day))
	}
}

// AuthorizeAgenda rechecks the current account, including revocation after JWT issuance.
func (s *Service) AuthorizeAgenda(ctx context.Context, role, caller, doctor string, write bool) error {
	actor, err := s.actor(ctx, role, caller)
	if err != nil {
		return err
	}
	if !actor.Enabled {
		return domain.ErrPatientNotActive
	}
	if role == "doctor" && caller == doctor {
		return nil
	}
	if !write && (role == "reception" || role == "admin") {
		return nil
	}
	return ErrForbidden
}
