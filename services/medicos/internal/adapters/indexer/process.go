// Package indexer keeps the search index in sync with MySQL by consuming
// medicos.perfil-actualizado.v1 and the Turnos booking events (ADR-006).
package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"portal-paciente-clinica/internal/platform/messaging"
	"portal-paciente-clinica/services/medicos/internal/app"
)

// ErrUnprocessable marks a message that retries cannot fix: it goes to the DLQ.
var ErrUnprocessable = errors.New("unprocessable message")

// Handler is the part of app.Service the indexer needs.
type Handler interface {
	IndexDoctor(ctx context.Context, id string) error
	HandleBooking(ctx context.Context, b app.Booking) (duplicate bool, err error)
}

type envelope struct {
	EventID    string          `json:"eventId"`
	OccurredAt time.Time       `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type bookingData struct {
	TurnID             string    `json:"turnId"`
	DoctorID           string    `json:"doctorId"`
	StartAt            time.Time `json:"startAt"`
	EndAt              time.Time `json:"endAt"`
	AppointmentVersion int       `json:"appointmentVersion"`
}

func unprocessable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnprocessable, fmt.Sprintf(format, args...))
}

// Process applies one event. result is "ok" or "duplicate"; occurredAt feeds
// search_index_lag_seconds. Errors wrapping ErrUnprocessable must not be retried.
func Process(ctx context.Context, h Handler, routingKey string, body []byte) (result string, occurredAt time.Time, err error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil || env.EventID == "" || len(env.Data) == 0 {
		return "", time.Time{}, unprocessable("invalid envelope")
	}
	switch routingKey {
	case app.RoutingProfileUpdated:
		var data struct {
			DoctorID string `json:"doctorId"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil || data.DoctorID == "" {
			return "", env.OccurredAt, unprocessable("profile event without doctorId")
		}
		err = h.IndexDoctor(ctx, data.DoctorID)
		return classify("ok", env.OccurredAt, err)
	case messaging.RoutingBooked, messaging.RoutingCancel:
		var data bookingData
		if err := json.Unmarshal(env.Data, &data); err != nil || data.TurnID == "" || data.DoctorID == "" ||
			data.StartAt.IsZero() || !data.EndAt.After(data.StartAt) || data.AppointmentVersion < 1 {
			return "", env.OccurredAt, unprocessable("invalid booking data")
		}
		dup, err := h.HandleBooking(ctx, app.Booking{TurnID: data.TurnID, DoctorID: data.DoctorID, Start: data.StartAt.UTC(),
			End: data.EndAt.UTC(), Active: routingKey == messaging.RoutingBooked, Version: data.AppointmentVersion})
		result := "ok"
		if dup {
			result = "duplicate"
		}
		return classify(result, env.OccurredAt, err)
	default:
		return "", env.OccurredAt, unprocessable("unexpected routing key %q", routingKey)
	}
}

func classify(result string, at time.Time, err error) (string, time.Time, error) {
	if errors.Is(err, app.ErrUnknownDoctor) {
		return "", at, fmt.Errorf("%w: %v", ErrUnprocessable, err)
	}
	if err != nil {
		return "", at, err
	}
	return result, at, nil
}
