package mysqlstore

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// Turnos validates bookings against its own doctor IDs; the Medicos seed must
// expose exactly the same ones or search would offer unbookable doctors.
func TestSeedDoctorIDsMatchTurnos(t *testing.T) {
	src, err := os.ReadFile("../../../../turnos/internal/adapters/directory/seed.go")
	if err != nil {
		t.Fatal(err)
	}
	var turnos []string
	for _, m := range regexp.MustCompile(`"(doc-\d+)":`).FindAllStringSubmatch(string(src), -1) {
		turnos = append(turnos, m[1])
	}
	var ours []string
	for _, d := range SeedDoctors {
		ours = append(ours, d.ID)
	}
	slices.Sort(turnos)
	slices.Sort(ours)
	if len(turnos) == 0 || !slices.Equal(turnos, ours) {
		t.Fatalf("Turnos doctors %v, Medicos seed %v", turnos, ours)
	}
	codes := map[string]bool{}
	for _, s := range SeedSpecialties {
		codes[s.Code] = true
	}
	for _, d := range SeedDoctors {
		for _, c := range d.Specialties {
			if !codes[c] {
				t.Fatalf("%s uses unknown specialty %s", d.ID, c)
			}
		}
	}
}
