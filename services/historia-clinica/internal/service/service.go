// Package service is the business layer of Historia clinica (layered
// architecture, ADR-002): api -> service -> repository (MongoDB, ADR-003).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/internal/platform/obs"
)

// MaxNoteLength is measured in characters (runes), not bytes.
const MaxNoteLength = 4000

var (
	ErrUnprocessable     = errors.New("unprocessable event")
	ErrInvalidNote       = errors.New("invalid note")
	ErrNotFound          = errors.New("encounter not found")
	ErrCancelled         = errors.New("encounter is cancelled")
	ErrAccessUnavailable = errors.New("clinical authorization unavailable")
	ErrForbidden         = errors.New("doctor is not assigned to this appointment")
	ErrNotEnabled        = errors.Join(ErrForbidden, errors.New("patient is not verified"))
	ErrTooEarly          = errors.New("appointment has not started")
)

const (
	StatusScheduled = "scheduled"
	StatusCancelled = "cancelled"
)

// Event is the envelope of docs/contracts/turnos-events-v1.schema.json.
type Event struct {
	EventID       string `json:"eventId"`
	EventType     string `json:"eventType"`
	Version       int    `json:"version"`
	OccurredAt    string `json:"occurredAt"`
	CorrelationID string `json:"correlationId"`
	Data          struct {
		TurnID             string `json:"turnId"`
		PatientID          string `json:"patientId"`
		DoctorID           string `json:"doctorId"`
		StartAt            string `json:"startAt"`
		EndAt              string `json:"endAt"`
		AppointmentVersion int    `json:"appointmentVersion"`
		ReasonCode         string `json:"reasonCode"`
	} `json:"data"`
}

// Change is what one event asks the repository to apply to an encounter.
type Change struct {
	EventID       string
	AppointmentID string
	PatientID     string
	DoctorID      string
	StartAt       time.Time
	Status        string
	CancelReason  string
	At            time.Time
}

// Note is a free-text clinical note appended by a doctor.
type Note struct {
	ID         string    `json:"id" bson:"id"`
	Text       string    `json:"text" bson:"text"`
	AuthorRole string    `json:"authorRole" bson:"authorRole"`
	CreatedAt  time.Time `json:"createdAt" bson:"createdAt"`
}

// Encounter is one entry of a patient's clinical timeline.
type Encounter struct {
	AppointmentID string    `json:"appointmentId" bson:"_id"`
	PatientID     string    `json:"patientId" bson:"patientId"`
	DoctorID      string    `json:"doctorId" bson:"doctorId"`
	StartAt       time.Time `json:"startAt" bson:"startAt"`
	Status        string    `json:"status" bson:"status"`
	CancelReason  string    `json:"cancelReason,omitempty" bson:"cancelReason,omitempty"`
	Notes         []Note    `json:"notes" bson:"notes"`
	UpdatedAt     time.Time `json:"updatedAt" bson:"updatedAt"`
}

// Store is the persistence port; the MongoDB repository implements it.
type Store interface {
	// Apply records the change once per EventID. duplicate=true means the
	// event was already processed and nothing changed. A cancellation is
	// terminal: a later (or reordered) booking never reverts it.
	Apply(ctx context.Context, c Change) (duplicate bool, err error)
	// AddNote returns ErrNotFound or ErrCancelled when the note cannot be added.
	AddNote(ctx context.Context, appointmentID string, n Note) error
	// Encounter returns the encounter for the appointment, or ErrNotFound.
	Encounter(ctx context.Context, appointmentID string) (Encounter, error)
	// Timeline returns the patient's encounters sorted by start, newest first.
	Timeline(ctx context.Context, patientID string) ([]Encounter, error)
	Ping(ctx context.Context) error
}

type AccessPolicy interface {
	Authorize(context.Context, string, string, string) error
}
type AccessFunc func(context.Context, string, string, string) error

func (f AccessFunc) Authorize(ctx context.Context, role, caller, patient string) error {
	return f(ctx, role, caller, patient)
}

type Service struct {
	Access AccessPolicy
	Store  Store
	Now    func() time.Time
	NewID  func() string
}

var statusByType = map[string]string{
	messaging.RoutingRescheduled: StatusScheduled,
	messaging.RoutingBooked:      StatusScheduled,
	messaging.RoutingCancel:      StatusCancelled,
}

// ParseEvent validates the envelope. An invalid event will not improve with
// retries, so it is reported as ErrUnprocessable and dead-lettered at once.
func ParseEvent(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return e, errors.Join(ErrUnprocessable, err)
	}
	_, known := statusByType[e.EventType]
	_, startErr := time.Parse(time.RFC3339, e.Data.StartAt)
	if !known || e.Version != 1 || !obs.ValidCorrelationID(e.EventID) || e.Data.TurnID == "" ||
		e.Data.PatientID == "" || e.Data.DoctorID == "" || startErr != nil {
		return e, ErrUnprocessable
	}
	if e.EventType == messaging.RoutingCancel && e.Data.ReasonCode == "" {
		return e, ErrUnprocessable
	}
	return e, nil
}

// HandleEvent projects a Turnos event onto the clinical timeline.
func (s *Service) HandleEvent(ctx context.Context, e Event) (bool, error) {
	start, _ := time.Parse(time.RFC3339, e.Data.StartAt)
	c := Change{EventID: e.EventID, AppointmentID: e.Data.TurnID, PatientID: e.Data.PatientID, DoctorID: e.Data.DoctorID,
		StartAt: start.UTC(), Status: statusByType[e.EventType], At: s.Now().UTC()}
	if c.Status == StatusCancelled {
		c.CancelReason = e.Data.ReasonCode
	}
	return s.Store.Apply(ctx, c)
}

// AddNote appends a clinical note; verifies doctor assignment and status.
func (s *Service) AddNote(ctx context.Context, appointmentID, doctorID, role, text string) (Note, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > MaxNoteLength {
		return Note{}, ErrInvalidNote
	}
	enc, err := s.Store.Encounter(ctx, appointmentID)
	if err != nil {
		return Note{}, err
	}
	if enc.DoctorID != doctorID {
		return Note{}, ErrForbidden
	}
	if enc.Status == StatusCancelled {
		return Note{}, ErrCancelled
	}
	if enc.StartAt.After(s.Now()) {
		return Note{}, ErrTooEarly
	}
	if s.Access == nil {
		return Note{}, ErrAccessUnavailable
	}
	if err := s.Access.Authorize(ctx, role, doctorID, enc.PatientID); err != nil {
		return Note{}, err
	}
	n := Note{ID: s.NewID(), Text: text, AuthorRole: role, CreatedAt: s.Now().UTC()}
	if err := s.Store.AddNote(ctx, appointmentID, n); err != nil {
		return Note{}, err
	}
	slog.InfoContext(ctx, "clinical note added", "event", "note.added", "appointment_id", appointmentID, "doctor_id", doctorID, "note_id", n.ID)
	return n, nil
}

func (s *Service) Timeline(ctx context.Context, patientID string) ([]Encounter, error) {
	return s.Store.Timeline(ctx, patientID)
}

func (s *Service) Ping(ctx context.Context) error { return s.Store.Ping(ctx) }

func (s *Service) TimelineFor(ctx context.Context, role, caller, patient string) ([]Encounter, error) {
	if s.Access == nil {
		return nil, ErrAccessUnavailable
	}
	if err := s.Access.Authorize(ctx, role, caller, patient); err != nil {
		return nil, err
	}
	return s.Store.Timeline(ctx, patient)
}
