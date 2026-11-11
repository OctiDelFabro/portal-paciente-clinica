package httpapi

import (
	"errors"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"testing"
	"time"
)

func TestScheduleRejectsInvalidAndOverflowingInputs(t *testing.T) {
	valid := scheduleDTO{DurationMinutes: 30, Weekdays: []time.Weekday{time.Monday}, Open: "09:00", Close: "13:00"}
	if _, err := valid.agenda("doc"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*scheduleDTO){func(d *scheduleDTO) { d.DurationMinutes = 9223372036854775807 }, func(d *scheduleDTO) { d.Open = "+9:00" }, func(d *scheduleDTO) { d.Close = "24:01" }, func(d *scheduleDTO) { d.Weekdays = []time.Weekday{time.Monday, time.Monday} }, func(d *scheduleDTO) { d.Open = "14:00" }} {
		input := valid
		mutate(&input)
		if _, err := input.agenda("doc"); !errors.Is(err, domain.ErrInvalidAgenda) {
			t.Fatalf("accepted invalid schedule: %+v %v", input, err)
		}
	}
}
