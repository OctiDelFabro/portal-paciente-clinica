package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

var fakeTariff = domain.Tariff{BaseCents: 2_500_000, CentsPerKWh: 18_000}

func (f fakeUC) Quote(_ context.Context, doctorID string, startAt time.Time) (app.Quote, error) {
	f.record("Quote:" + doctorID)
	return app.Quote{DoctorID: doctorID, StartAt: startAt, Price: fakeTariff.Price(f.energy)}, f.err
}

func (f fakeUC) PriceOf(e domain.Energy) domain.Price { return fakeTariff.Price(e) }

var estimated = fakeTariff.Estimated(381_000, "consumo-v1")

func TestQuoteReturnsThePriceBreakdown(t *testing.T) {
	rec := serve(fakeUC{energy: estimated}, "GET", "/doctors/doc-001/quote?startAt=2026-12-01T10:00:00-03:00", "", "pat-1")
	want := `{"doctorId":"doc-001","startAt":"2026-12-01T13:00:00Z","basePriceArs":25000.00,` +
		`"energy":{"status":"estimated","kWh":0.381,"costArs":68.58,"algorithmVersion":"consumo-v1"},"totalArs":25068.58}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("got %d\n%s\nwant\n%s", rec.Code, rec.Body, want)
	}
}

func TestQuoteSaysWhenTheEnergyCostIsPending(t *testing.T) {
	rec := serve(fakeUC{energy: domain.Energy{Status: domain.EnergyPending}}, "GET", "/doctors/doc-001/quote?startAt=2026-12-01T13:00:00Z", "", "")
	body := rec.Body.String()
	for _, part := range []string{`"status":"pending"`, `"kWh":null`, `"costArs":null`, `"algorithmVersion":null`, `"message":"Costo energético pendiente`, `"totalArs":25000.00`} {
		if !strings.Contains(body, part) {
			t.Errorf("missing %s in %s", part, body)
		}
	}
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestQuoteValidatesItsInput(t *testing.T) {
	if rec := serve(fakeUC{}, "GET", "/doctors/doc-001/quote?startAt=tomorrow", "", ""); rec.Code != 400 || errorCode(t, rec) != "INVALID_REQUEST" {
		t.Fatalf("bad startAt: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(fakeUC{err: app.ErrUnknownDoctor}, "GET", "/doctors/nope/quote?startAt=2026-12-01T13:00:00Z", "", ""); rec.Code != 404 || errorCode(t, rec) != "UNKNOWN_DOCTOR" {
		t.Fatalf("unknown doctor: %d %s", rec.Code, rec.Body)
	}
}

func TestPortalAppointmentResponsesIncludeThePrice(t *testing.T) {
	uc := fakeUC{energy: estimated}
	for _, r := range [][3]string{
		{"POST", "/appointments", valid},
		{"GET", "/appointments", ""},
		{"GET", "/appointments/a-1", ""},
		{"POST", "/appointments/a-1/cancel", ""},
	} {
		rec := serve(uc, r[0], r[1], r[2], "pat-001")
		if rec.Code >= 300 || !strings.Contains(rec.Body.String(), `"price":{"basePriceArs":25000.00,"energy":{"status":"estimated","kWh":0.381,"costArs":68.58,"algorithmVersion":"consumo-v1"},"totalArs":25068.58}`) {
			t.Errorf("%s %s: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
}

// Previous behavior: the booking body keeps every existing field unchanged.
func TestPortalBookingKeepsTheExistingFields(t *testing.T) {
	rec := serve(fakeUC{energy: domain.Energy{Status: domain.EnergyPending}}, "POST", "/appointments", valid, "pat-001")
	want := `{"id":"a-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-10-25T10:00:00-03:00","endAt":"2026-10-25T10:30:00-03:00","status":"booked","version":1,"price":`
	if rec.Code != 201 || !strings.HasPrefix(rec.Body.String(), want) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

// The partner contract (turnos-partner-v1) is byte-for-byte unchanged: no price.
func TestPartnerBookingBodyIsUnchanged(t *testing.T) {
	f := &fakePartner{}
	rec := call(partnerMux(f, 0), "POST", "/v1/appointments", bookBody, bookHeaders())
	want := `{"id":"a1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-01T10:00:00Z","endAt":"2026-12-01T10:30:00Z","status":"booked","version":1}` + "\n"
	if rec.Code != 201 || rec.Body.String() != want {
		t.Fatalf("got %d\n%s\nwant\n%s", rec.Code, rec.Body, want)
	}
}
