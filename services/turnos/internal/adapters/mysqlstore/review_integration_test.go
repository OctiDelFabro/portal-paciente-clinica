//go:build integration

package mysqlstore

import (
	"context"
	"fmt"
	"portal-paciente-clinica/services/turnos/internal/adapters/directory"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestReviewPaginationUnicodeReasonAndFrozenTariff(t *testing.T) {
	store := startStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	tariff := domain.Tariff{BaseCents: 2500000, CentsPerKWh: 18000}
	svc := &app.Service{Store: store, Directory: directory.Seed{}, Now: func() time.Time { return now }, NewID: func() string { return "00000000-0000-4000-d000-000000000001" }, Tariff: tariff}
	a, err := svc.Book(ctx, "pat-001", "doc-001", nextWorkday(now).Add(9*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		err = store.InTx(ctx, func(tx app.Tx) error {
			return tx.Insert(ctx, domain.Appointment{ID: fmt.Sprintf("00000000-0000-4000-d000-%012d", i+2), PatientID: "pat-001", DoctorID: "doc-001", StartAt: a.StartAt.AddDate(0, 0, i+2), EndAt: a.EndAt.AddDate(0, 0, i+2), Status: domain.StatusCancelled, Version: 1, Tariff: &tariff})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []struct{ patient, doctor string }{{"pat-001", ""}, {"", "doc-001"}} {
		page, more, err := store.AppointmentsPage(ctx, scope.patient, scope.doctor, 1, now)
		if err != nil || len(page) != 100 || !more || page[0].ID != a.ID {
			t.Fatalf("imminent active appointment hidden: count=%d more=%v err=%v", len(page), more, err)
		}
		next, more, err := store.AppointmentsPage(ctx, scope.patient, scope.doctor, 2, now)
		if err != nil || len(next) != 6 || more {
			t.Fatalf("history truncated: count=%d more=%v err=%v", len(next), more, err)
		}
	}
	stored, err := store.Get(ctx, a.ID)
	if err != nil || stored.Tariff == nil || *stored.Tariff != tariff {
		t.Fatalf("tariff not stored: %+v %v", stored.Tariff, err)
	}
	before := svc.PriceOf(stored)
	svc.Tariff.BaseCents = 3000000
	if svc.PriceOf(stored) != before {
		t.Fatal("confirmed base price changed")
	}
	reason := strings.Repeat("ó", 200)
	if _, err = svc.Cancel(ctx, a.ID, reason); err != nil {
		t.Fatal(err)
	}
	stored, err = store.Get(ctx, a.ID)
	if err != nil || stored.CancelReason != reason {
		t.Fatalf("Unicode reason not persisted: %v", err)
	}
}
