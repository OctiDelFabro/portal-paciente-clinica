package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNextFreeSkipsContiguousBookings(t *testing.T) {
	from := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 12, 1, h, m, 0, 0, time.UTC) }
	tests := []struct {
		name string
		busy []Interval
		want time.Time
	}{
		{"no bookings", nil, from},
		{"booking later leaves from free", []Interval{{at(11, 0), at(11, 30)}}, from},
		{"booking covering from", []Interval{{at(9, 45), at(10, 30)}}, at(10, 30)},
		{"chained bookings in any order", []Interval{{at(10, 30), at(11, 0)}, {at(10, 0), at(10, 30)}, {at(12, 0), at(12, 30)}}, at(11, 0)},
		{"booking ending exactly at from", []Interval{{at(9, 30), at(10, 0)}}, from},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextFree(from, tt.busy); !got.Equal(tt.want) {
				t.Fatalf("want %s, got %s", tt.want, got)
			}
		})
	}
}

func TestQueryValidate(t *testing.T) {
	tests := []struct {
		name  string
		q     Query
		valid bool
	}{
		{"defaults", Query{Page: 1, Size: 10}, true},
		{"max size", Query{Page: 1, Size: 50}, true},
		{"size over 50", Query{Page: 1, Size: 51}, false},
		{"size zero", Query{Page: 1, Size: 0}, false},
		{"page zero", Query{Page: 0, Size: 10}, false},
		{"window exactly 1000", Query{Page: 20, Size: 50}, true},
		{"window over 1000", Query{Page: 21, Size: 50}, false},
		{"sort by next available", Query{Page: 1, Size: 10, Sort: SortNextAvailable}, true},
		{"unknown sort", Query{Page: 1, Size: 10, Sort: "price"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.q.Validate()
			var inv *InvalidError
			if tt.valid != (err == nil) || (err != nil && !errors.As(err, &inv)) {
				t.Fatalf("valid=%v, got %v", tt.valid, err)
			}
		})
	}
}

func TestValidateProfile(t *testing.T) {
	ok := Profile{FullName: "Laura Gomez", Specialties: []string{"cardiologia"}}
	tests := []struct {
		name  string
		p     Profile
		valid bool
	}{
		{"valid", ok, true},
		{"blank name", Profile{FullName: "  ", Specialties: []string{"cardiologia"}}, false},
		{"no specialties", Profile{FullName: "Laura Gomez"}, false},
		{"duplicate specialties", Profile{FullName: "Laura Gomez", Specialties: []string{"a", "a"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.p.Validate(); tt.valid != (err == nil) {
				t.Fatalf("valid=%v, got %v", tt.valid, err)
			}
		})
	}
}
