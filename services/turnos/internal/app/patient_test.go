package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

func TestBookForPatientRejectsPatientsMissingFromTheDirectory(t *testing.T) {
	svc, store := newService()
	if _, err := svc.BookForPatient(context.Background(), "unknown-7", "d-1", fixedNow.Add(48*time.Hour)); !errors.Is(err, ErrUnknownPatient) {
		t.Fatalf("want ErrUnknownPatient, got %v", err)
	}
	if len(store.outbox) != 0 {
		t.Fatal("rejected identity created an event")
	}
}

func TestBookForPatientKeepsDisabledPatientsRejected(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.BookForPatient(context.Background(), "disabled-1", "d-1", fixedNow.Add(48*time.Hour)); !errors.Is(err, domain.ErrPatientNotActive) {
		t.Fatalf("want ErrPatientNotActive, got %v", err)
	}
}

// Previous behavior: without gateway authentication the directory decides.
func TestBookStillRejectsPatientsMissingFromTheDirectory(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Book(context.Background(), "unknown-7", "d-1", fixedNow.Add(48*time.Hour)); !errors.Is(err, ErrUnknownPatient) {
		t.Fatalf("want ErrUnknownPatient, got %v", err)
	}
	if _, _, err := svc.BookForPartner(context.Background(), "carcruds", "k-1", "unknown-7", "d-1", fixedNow.Add(48*time.Hour)); !errors.Is(err, ErrUnknownPatient) {
		t.Fatalf("partner flow: want ErrUnknownPatient, got %v", err)
	}
}

func TestListForPatientReturnsOnlyTheirAppointments(t *testing.T) {
	svc, _ := newService()
	mine, _ := svc.BookForPatient(context.Background(), "p-1", "d-1", fixedNow.Add(48*time.Hour))
	_, _ = svc.Book(context.Background(), "p-2", "d-1", fixedNow.Add(49*time.Hour))
	list, err := svc.ListForPatient(context.Background(), "p-1")
	if err != nil || len(list) != 1 || list[0].ID != mine.ID {
		t.Fatalf("want only %s, got %+v err=%v", mine.ID, list, err)
	}
}

func TestPatientCannotReadOrCancelAnotherPatientsAppointment(t *testing.T) {
	svc, store := newService()
	other, _ := svc.Book(context.Background(), "p-2", "d-1", fixedNow.Add(48*time.Hour))
	if _, err := svc.GetForPatient(context.Background(), "p-1", other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get: want ErrNotFound, got %v", err)
	}
	if _, err := svc.CancelForPatient(context.Background(), "p-1", other.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel: want ErrNotFound, got %v", err)
	}
	if store.rows[other.ID].Status != domain.StatusBooked {
		t.Fatal("appointment of another patient was cancelled")
	}
	if c, err := svc.CancelForPatient(context.Background(), "p-2", other.ID, ""); err != nil || c.Status != domain.StatusCancelled {
		t.Fatalf("owner cancel failed: %v", err)
	}
}
