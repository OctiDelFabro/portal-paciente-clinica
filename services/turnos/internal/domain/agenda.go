package domain

import (
	"errors"
	"time"
)

var ErrInvalidAgenda = errors.New("invalid schedule or block")
var ErrAgendaConflict = errors.New("existing appointments would fall outside schedule")

type Agenda struct {
	DoctorID string
	Duration time.Duration
	Schedule Schedule
}

func (a Agenda) Validate() error {
	if a.DoctorID == "" || a.Duration < 5*time.Minute || a.Duration > 4*time.Hour || a.Duration%time.Minute != 0 || len(a.Schedule.Weekdays) == 0 || a.Schedule.Open < 0 || a.Schedule.Close > 24*time.Hour || a.Schedule.Close <= a.Schedule.Open || a.Duration > a.Schedule.Close-a.Schedule.Open {
		return ErrInvalidAgenda
	}
	seen := map[time.Weekday]bool{}
	for _, day := range a.Schedule.Weekdays {
		if day < time.Sunday || day > time.Saturday || seen[day] {
			return ErrInvalidAgenda
		}
		seen[day] = true
	}
	return nil
}

type Block struct {
	ID       string    `json:"id"`
	DoctorID string    `json:"doctorId"`
	StartAt  time.Time `json:"startAt"`
	EndAt    time.Time `json:"endAt"`
	Reason   string    `json:"reason"`
}

// Blocks use the same half-open intervals as bookings.
func WithBlocks(existing []Appointment, blocks []Block) []Appointment {
	out := append([]Appointment(nil), existing...)
	for _, b := range blocks {
		out = append(out, Appointment{ID: "block:" + b.ID, DoctorID: b.DoctorID, StartAt: b.StartAt, EndAt: b.EndAt, Status: StatusBooked})
	}
	return out
}
