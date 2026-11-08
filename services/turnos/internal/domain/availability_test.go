package domain

import (
	"errors"
	"testing"
	"time"
)

// 2026-10-26 is a Monday in the clinic zone.
var (
	monday    = time.Date(2026, 10, 26, 0, 0, 0, 0, ClinicZone)
	weekdays  = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	scheduled = Doctor{ID: "d-1", Duration: 30 * time.Minute,
		Schedule: Schedule{Weekdays: weekdays, Open: 9 * time.Hour, Close: 11 * time.Hour}}
)

func TestFreeSlotsSplitsTheScheduleByDuration(t *testing.T) {
	slots, err := FreeSlots(scheduled, monday, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 4 {
		t.Fatalf("want 4 slots between 09:00 and 11:00, got %d: %v", len(slots), slots)
	}
	if got := slots[0].StartAt.Format(time.RFC3339); got != "2026-10-26T09:00:00-03:00" {
		t.Fatalf("first slot starts at %s", got)
	}
	if got := slots[3].EndAt.Format(time.RFC3339); got != "2026-10-26T11:00:00-03:00" {
		t.Fatalf("last slot ends at %s", got)
	}
}

func TestFreeSlotsExcludesActiveAppointmentsOfTheDoctor(t *testing.T) {
	nine := monday.Add(9 * time.Hour)
	existing := []Appointment{
		// Partially overlaps 09:30 and 10:00.
		{ID: "x", DoctorID: "d-1", PatientID: "p-9", StartAt: nine.Add(45 * time.Minute), EndAt: nine.Add(75 * time.Minute), Status: StatusBooked},
		// Cancelled appointments free their slot.
		{ID: "y", DoctorID: "d-1", PatientID: "p-9", StartAt: nine, EndAt: nine.Add(30 * time.Minute), Status: StatusCancelled},
	}
	slots, _ := FreeSlots(scheduled, monday, now, existing)
	var starts []string
	for _, s := range slots {
		starts = append(starts, s.StartAt.Format("15:04"))
	}
	if len(starts) != 2 || starts[0] != "09:00" || starts[1] != "10:30" {
		t.Fatalf("want 09:00 and 10:30 free, got %v", starts)
	}
}

func TestFreeSlotsHonoursTheMinimumLeadTime(t *testing.T) {
	// 24 h before 10:00 local: 09:00 and 09:30 are too close, 10:00 is exactly 24 h ahead.
	current := monday.Add(-14 * time.Hour) // Sunday 10:00 local
	slots, _ := FreeSlots(scheduled, monday, current, nil)
	if len(slots) != 2 || slots[0].StartAt.Format("15:04") != "10:00" {
		t.Fatalf("want 10:00 and 10:30, got %v", slots)
	}
}

func TestFreeSlotsIsEmptyOutsideWorkingDays(t *testing.T) {
	slots, err := FreeSlots(scheduled, monday.AddDate(0, 0, -1), now, nil)
	if err != nil || len(slots) != 0 {
		t.Fatalf("Sunday must have no slots, got %v err=%v", slots, err)
	}
}

func TestFreeSlotsRejectsDoctorWithoutDuration(t *testing.T) {
	d := scheduled
	d.Duration = 0
	if _, err := FreeSlots(d, monday, now, nil); !errors.Is(err, ErrInvalidDuration) {
		t.Fatalf("want ErrInvalidDuration, got %v", err)
	}
}

// A slot shown as free must be accepted by Book under the same appointments.
func TestEveryFreeSlotIsBookable(t *testing.T) {
	nine := monday.Add(9 * time.Hour)
	existing := []Appointment{{ID: "x", DoctorID: "d-1", PatientID: "p-9", StartAt: nine.Add(30 * time.Minute), EndAt: nine.Add(time.Hour), Status: StatusBooked}}
	slots, _ := FreeSlots(scheduled, monday, now, existing)
	for _, s := range slots {
		a, err := Book("a", patient, scheduled, s.StartAt, now, existing)
		if err != nil || !a.EndAt.Equal(s.EndAt) {
			t.Fatalf("slot %v not bookable: %v", s, err)
		}
	}
}

func TestDayOfUsesTheClinicZone(t *testing.T) {
	// 01:00 UTC on the 27th is still the 26th in Buenos Aires.
	if got := DayOf(time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)); got != "2026-10-26" {
		t.Fatalf("got %s", got)
	}
	if d, err := ParseDay("2026-10-26"); err != nil || !d.Equal(monday) {
		t.Fatalf("ParseDay: %v %v", d, err)
	}
	for _, bad := range []string{"", "2026-13-01", "26-10-2026", "2026-10-26T00:00:00Z"} {
		if _, err := ParseDay(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
