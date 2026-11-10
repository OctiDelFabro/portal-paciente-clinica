package domain

import (
	"fmt"
	"slices"
	"time"
)

// ClinicZone is the clinic's local time (Argentina, UTC-03:00, no daylight
// saving). Schedules and availability days are expressed in it. A fixed zone
// avoids depending on the tz database inside the container image.
var ClinicZone = time.FixedZone("ART", -3*60*60)

const dayLayout = "2006-01-02"

// Schedule is a doctor's weekly working window: the same hours on each listed
// weekday, as offsets from local midnight. Reads and writes enforce it.
type Schedule struct {
	Weekdays    []time.Weekday
	Open, Close time.Duration
}

// Slot is a bookable interval [StartAt, EndAt) of one doctor.
type Slot struct {
	StartAt time.Time
	EndAt   time.Time
}

// DayOf returns the clinic-local day of t as YYYY-MM-DD.
func DayOf(t time.Time) string { return t.In(ClinicZone).Format(dayLayout) }

// ParseDay parses YYYY-MM-DD and returns local midnight of that day.
func ParseDay(s string) (time.Time, error) {
	d, err := time.ParseInLocation(dayLayout, s, ClinicZone)
	if err != nil || d.Format(dayLayout) != s {
		return time.Time{}, fmt.Errorf("invalid day %q: want YYYY-MM-DD", s)
	}
	return d, nil
}

// FreeSlots splits the doctor's schedule for day (local midnight) into slots of
// the doctor's duration and keeps those that Book would accept for any enabled
// patient without other appointments: at least MinLeadTime ahead of now and not
// overlapping an active appointment of the doctor in existing.
func FreeSlots(doctor Doctor, day, now time.Time, existing []Appointment) ([]Slot, error) {
	if doctor.Duration <= 0 {
		return nil, ErrInvalidDuration
	}
	day = day.In(ClinicZone)
	slots := []Slot{}
	if !slices.Contains(doctor.Schedule.Weekdays, day.Weekday()) {
		return slots, nil
	}
	closeAt := day.Add(doctor.Schedule.Close)
	for start := day.Add(doctor.Schedule.Open); !start.Add(doctor.Duration).After(closeAt); start = start.Add(doctor.Duration) {
		end := start.Add(doctor.Duration)
		if start.Sub(now) < MinLeadTime || overlapsDoctor(doctor.ID, start, end, existing) {
			continue
		}
		slots = append(slots, Slot{StartAt: start, EndAt: end})
	}
	return slots, nil
}

func overlapsDoctor(doctorID string, start, end time.Time, existing []Appointment) bool {
	for _, a := range existing {
		if a.DoctorID == doctorID && a.Overlaps(start, end) {
			return true
		}
	}
	return false
}

// Allows accepts starts inside the working window with the configured duration.
// The API may start a consultation between grid boundaries; the grid is a UI
// suggestion, while the authoritative constraints are hours and no overlap.
func (s Schedule) Allows(start time.Time, duration time.Duration) bool {
	local := start.In(ClinicZone)
	if duration <= 0 || !slices.Contains(s.Weekdays, local.Weekday()) || s.Open < 0 || s.Close > 24*time.Hour || s.Close <= s.Open {
		return false
	}
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, ClinicZone)
	return !local.Before(midnight.Add(s.Open)) && !local.Add(duration).After(midnight.Add(s.Close))
}
