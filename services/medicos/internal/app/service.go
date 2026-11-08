// Package app orchestrates the Medicos use cases and defines the ports that
// adapters (MySQL, OpenSearch, HTTP, AMQP) implement (ADR-002, ADR-006).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

// RoutingProfileUpdated is the routing key of the profile change event.
const RoutingProfileUpdated = "medicos.perfil-actualizado.v1"

var (
	ErrNotFound          = errors.New("doctor not found")
	ErrUnknownSpecialty  = errors.New("unknown specialty")
	ErrUnknownDoctor     = errors.New("event references an unknown doctor")
	ErrSearchUnavailable = errors.New("search engine unavailable")
)

// OutboxMessage is an event saved in the same transaction as the change (ADR-005).
type OutboxMessage struct {
	EventID    string
	RoutingKey string
	Payload    []byte
	Headers    map[string]string
}

// Booking is the Medicos projection of one Turnos appointment, used only to
// derive nextAvailableAt. Version is Turnos' appointmentVersion.
type Booking struct {
	TurnID   string
	DoctorID string
	Start    time.Time
	End      time.Time
	Active   bool
	Version  int
}

type Tx interface {
	GetForUpdate(ctx context.Context, id string) (domain.Doctor, error)
	SpecialtiesExist(ctx context.Context, codes []string) (bool, error)
	SaveProfile(ctx context.Context, d domain.Doctor) error
	AppendOutbox(ctx context.Context, m OutboxMessage) error
}

type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
	Doctor(ctx context.Context, id string) (domain.Doctor, error)
	Doctors(ctx context.Context) ([]domain.Doctor, error)
	Specialties(ctx context.Context) ([]domain.Specialty, error)
	// ApplyBooking stores b unless a version >= b.Version is already stored;
	// it reports whether anything changed.
	ApplyBooking(ctx context.Context, b Booking) (bool, error)
	BusyIntervals(ctx context.Context, doctorID string, from time.Time) ([]domain.Interval, error)
}

// Index is the derived search projection. Implementations return
// ErrSearchUnavailable when the engine cannot be reached.
type Index interface {
	Upsert(ctx context.Context, doc domain.SearchDoc) error
	Search(ctx context.Context, q domain.Query) (domain.Page, error)
	// Rebuild writes docs into a new index version and swaps the alias; it
	// returns the new index name.
	Rebuild(ctx context.Context, docs []domain.SearchDoc) (string, error)
}

type Service struct {
	Store Store
	Index Index
	Now   func() time.Time
	NewID func() string
}

// UpdateProfile saves the profile with the next version and the change event
// in one transaction, so the index can never miss a committed change.
func (s *Service) UpdateProfile(ctx context.Context, id string, p domain.Profile) (domain.Doctor, error) {
	if err := p.Validate(); err != nil {
		return domain.Doctor{}, err
	}
	var saved domain.Doctor
	err := s.Store.InTx(ctx, func(tx Tx) error {
		cur, err := tx.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		ok, err := tx.SpecialtiesExist(ctx, p.Specialties)
		if err != nil {
			return err
		}
		if !ok {
			return ErrUnknownSpecialty
		}
		saved = domain.Doctor{ID: cur.ID, FullName: p.FullName, Specialties: p.Specialties,
			AcceptsMinors: p.AcceptsMinors, Active: p.Active, ProfileVersion: cur.ProfileVersion + 1}
		if err := tx.SaveProfile(ctx, saved); err != nil {
			return err
		}
		return tx.AppendOutbox(ctx, s.profileEvent(ctx, saved))
	})
	if err != nil {
		return domain.Doctor{}, err
	}
	return saved, nil
}

func (s *Service) Doctor(ctx context.Context, id string) (domain.Doctor, error) {
	return s.Store.Doctor(ctx, id)
}

func (s *Service) Specialties(ctx context.Context) ([]domain.Specialty, error) {
	return s.Store.Specialties(ctx)
}

func (s *Service) Search(ctx context.Context, q domain.Query) (domain.Page, error) {
	if err := q.Validate(); err != nil {
		return domain.Page{}, err
	}
	return s.Index.Search(ctx, q)
}

// IndexDoctor writes the current MySQL state of a doctor into the index.
// Reading the authoritative state (instead of the event payload) makes every
// redelivery or out-of-order event converge to the latest profile.
func (s *Service) IndexDoctor(ctx context.Context, id string) error {
	d, err := s.Store.Doctor(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrUnknownDoctor
	}
	if err != nil {
		return err
	}
	doc, err := s.searchDoc(ctx, d)
	if err != nil {
		return err
	}
	return s.Index.Upsert(ctx, doc)
}

// HandleBooking applies a Turnos booking/cancellation to the projection and
// reindexes the doctor. duplicate is true when the event changed nothing.
func (s *Service) HandleBooking(ctx context.Context, b Booking) (duplicate bool, err error) {
	if _, err := s.Store.Doctor(ctx, b.DoctorID); errors.Is(err, ErrNotFound) {
		return false, ErrUnknownDoctor
	} else if err != nil {
		return false, err
	}
	changed, err := s.Store.ApplyBooking(ctx, b)
	if err != nil || !changed {
		return !changed && err == nil, err
	}
	return false, s.IndexDoctor(ctx, b.DoctorID)
}

// Reindex rebuilds the whole index from MySQL into a new version.
func (s *Service) Reindex(ctx context.Context) (index string, count int, err error) {
	doctors, err := s.Store.Doctors(ctx)
	if err != nil {
		return "", 0, err
	}
	docs := make([]domain.SearchDoc, 0, len(doctors))
	for _, d := range doctors {
		doc, err := s.searchDoc(ctx, d)
		if err != nil {
			return "", 0, err
		}
		docs = append(docs, doc)
	}
	index, err = s.Index.Rebuild(ctx, docs)
	return index, len(docs), err
}

func (s *Service) searchDoc(ctx context.Context, d domain.Doctor) (domain.SearchDoc, error) {
	from := s.Now().UTC().Add(domain.MinLeadTime).Truncate(time.Second)
	busy, err := s.Store.BusyIntervals(ctx, d.ID, from)
	if err != nil {
		return domain.SearchDoc{}, fmt.Errorf("busy intervals: %w", err)
	}
	next := domain.NextFree(from, busy)
	return domain.SearchDoc{DoctorID: d.ID, FullName: d.FullName, Specialties: d.Specialties, AcceptsMinors: d.AcceptsMinors,
		Active: d.Active, NextAvailableAt: &next, ProfileVersion: d.ProfileVersion}, nil
}

// ProfileEnvelope follows the same envelope as the Turnos events (eventId,
// eventType, version, occurredAt, correlationId, data).
type ProfileEnvelope struct {
	EventID       string      `json:"eventId"`
	EventType     string      `json:"eventType"`
	Version       int         `json:"version"`
	OccurredAt    string      `json:"occurredAt"`
	CorrelationID string      `json:"correlationId"`
	Data          ProfileData `json:"data"`
}

type ProfileData struct {
	DoctorID       string   `json:"doctorId"`
	FullName       string   `json:"fullName"`
	Specialties    []string `json:"specialties"`
	AcceptsMinors  bool     `json:"acceptsMinors"`
	Active         bool     `json:"active"`
	ProfileVersion int64    `json:"profileVersion"`
}

func (s *Service) profileEvent(ctx context.Context, d domain.Doctor) OutboxMessage {
	correlation := obs.CorrelationID(ctx)
	if correlation == "" {
		correlation = s.NewID()
	}
	env := ProfileEnvelope{
		EventID: s.NewID(), EventType: RoutingProfileUpdated, Version: 1,
		OccurredAt: s.Now().UTC().Format(time.RFC3339Nano), CorrelationID: correlation,
		Data: ProfileData{DoctorID: d.ID, FullName: d.FullName, Specialties: d.Specialties,
			AcceptsMinors: d.AcceptsMinors, Active: d.Active, ProfileVersion: d.ProfileVersion},
	}
	payload, _ := json.Marshal(env)
	// The trace context is stored so the asynchronous relay continues the same trace.
	headers := map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))
	headers["x-correlation-id"] = correlation
	return OutboxMessage{EventID: env.EventID, RoutingKey: RoutingProfileUpdated, Payload: payload, Headers: headers}
}
