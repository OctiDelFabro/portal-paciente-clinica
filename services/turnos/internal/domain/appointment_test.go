package domain

import (
	"errors"
	"testing"
	"time"
)

var (
	now     = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	patient = Patient{ID: "p-1", Enabled: true}
	doctor  = Doctor{ID: "d-1", Duration: 30 * time.Minute}
)

func TestBookRejectsLessThan24Hours(t *testing.T) {
	_, err := Book("a-1", patient, doctor, now.Add(23*time.Hour+59*time.Minute), now, nil)
	if !errors.Is(err, ErrLeadTime) {
		t.Fatalf("want ErrLeadTime, got %v", err)
	}
}

func TestBookAcceptsExactly24Hours(t *testing.T) {
	a, err := Book("a-1", patient, doctor, now.Add(24*time.Hour), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusBooked || a.Version != 1 || !a.EndAt.Equal(now.Add(24*time.Hour+30*time.Minute)) {
		t.Fatalf("unexpected appointment: %+v", a)
	}
}

func TestBookRejectsDoctorOverlapWithDifferentDuration(t *testing.T) {
	start := now.Add(48 * time.Hour)
	existing := []Appointment{{ID: "x", PatientID: "p-9", DoctorID: "d-1", StartAt: start.Add(-20 * time.Minute), EndAt: start.Add(10 * time.Minute), Status: StatusBooked}}
	if _, err := Book("a-1", patient, doctor, start, now, existing); !errors.Is(err, ErrOverlap) {
		t.Fatalf("want ErrOverlap, got %v", err)
	}
}

func TestBookRejectsPatientOverlapWithOtherDoctor(t *testing.T) {
	start := now.Add(48 * time.Hour)
	existing := []Appointment{{ID: "x", PatientID: "p-1", DoctorID: "d-2", StartAt: start.Add(15 * time.Minute), EndAt: start.Add(45 * time.Minute), Status: StatusBooked}}
	if _, err := Book("a-1", patient, doctor, start, now, existing); !errors.Is(err, ErrOverlap) {
		t.Fatalf("want ErrOverlap, got %v", err)
	}
}

func TestBookAllowsAdjacentAndCancelledSlots(t *testing.T) {
	start := now.Add(48 * time.Hour)
	existing := []Appointment{
		{ID: "before", PatientID: "p-9", DoctorID: "d-1", StartAt: start.Add(-30 * time.Minute), EndAt: start, Status: StatusBooked},
		{ID: "cancelled", PatientID: "p-1", DoctorID: "d-1", StartAt: start, EndAt: start.Add(30 * time.Minute), Status: StatusCancelled},
	}
	if _, err := Book("a-1", patient, doctor, start, now, existing); err != nil {
		t.Fatalf("adjacent or cancelled slots must not block: %v", err)
	}
}

func TestBookRejectsPatientNotEnabled(t *testing.T) {
	_, err := Book("a-1", Patient{ID: "p-2"}, doctor, now.Add(48*time.Hour), now, nil)
	if !errors.Is(err, ErrPatientNotActive) {
		t.Fatalf("want ErrPatientNotActive, got %v", err)
	}
}

func TestCancelRules(t *testing.T) {
	a, _ := Book("a-1", patient, doctor, now.Add(48*time.Hour), now, nil)
	if _, err := a.Cancel("patient_request", now.Add(25*time.Hour)); !errors.Is(err, ErrLeadTime) {
		t.Fatalf("cancel with less than 24h must fail, got %v", err)
	}
	cancelled, err := a.Cancel("patient_request", now)
	if err != nil || cancelled.Status != StatusCancelled || cancelled.Version != 2 {
		t.Fatalf("cancel failed: %+v %v", cancelled, err)
	}
	if _, err := cancelled.Cancel("again", now); !errors.Is(err, ErrAlreadyCancelled) {
		t.Fatalf("want ErrAlreadyCancelled, got %v", err)
	}
}
