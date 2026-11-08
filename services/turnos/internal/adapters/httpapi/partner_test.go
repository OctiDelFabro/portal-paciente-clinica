package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/apikey"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type fakePartner struct {
	err      error
	replayed bool
	consumer string
	key      string
	month    time.Time
}

var partnerAppt = domain.Appointment{ID: "a1", PatientID: "pat-001", DoctorID: "doc-001",
	StartAt: time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC), EndAt: time.Date(2026, 12, 1, 10, 30, 0, 0, time.UTC),
	Status: domain.StatusBooked, Version: 1}

func (f *fakePartner) BookForPartner(_ context.Context, consumer, key, p, d string, s time.Time) (domain.Appointment, bool, error) {
	f.consumer, f.key = consumer, key
	return partnerAppt, f.replayed, f.err
}
func (f *fakePartner) GetForPartner(_ context.Context, consumer, id string) (domain.Appointment, error) {
	f.consumer = consumer
	return partnerAppt, f.err
}
func (f *fakePartner) CancelForPartner(_ context.Context, consumer, id string) (domain.Appointment, error) {
	f.consumer = consumer
	a := partnerAppt
	a.Status, a.Version, a.CancelReason = domain.StatusCancelled, 2, app.PartnerCancelReason
	return a, f.err
}
func (f *fakePartner) Usage(_ context.Context, consumer string, month time.Time) (app.Usage, error) {
	f.consumer, f.month = consumer, month
	from := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	return app.Usage{Consumer: consumer, From: from, To: from.AddDate(0, 1, 0),
		Items:  []app.UsageItem{{AppointmentID: "a1", BookedAt: from.Add(time.Hour), PriceCents: 2500000, Currency: "ARS", Status: domain.StatusCancelled}},
		Totals: []app.UsageTotal{{Currency: "ARS", Bookings: 1, AmountCents: 2500000}}}, f.err
}
func (f *fakePartner) Doctors(context.Context) ([]domain.Doctor, error) {
	return []domain.Doctor{{ID: "doc-001", Duration: 30 * time.Minute}}, f.err
}

var partnerNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func partnerMux(f *fakePartner, rate int) *http.ServeMux {
	mux := http.NewServeMux()
	(&Partner{UseCases: f, Keys: map[string]string{"carcruds": "secret-1"}, Limiter: &apikey.Limiter{Rate: rate},
		Price: app.Pricing{Cents: 2500000, Currency: "ARS"}, Now: func() time.Time { return partnerNow }}).Register(mux)
	return mux
}

const bookBody = `{"patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-01T10:00:00Z"}`

func call(mux http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func bookHeaders() map[string]string {
	return map[string]string{"X-API-Key": "secret-1", "Idempotency-Key": "chequeo-001", "Content-Type": "application/json"}
}

func TestPartnerRoutesRequireAValidKey(t *testing.T) {
	mux := partnerMux(&fakePartner{}, 0)
	routes := [][2]string{{"POST", "/v1/appointments"}, {"GET", "/v1/appointments/a1"}, {"POST", "/v1/appointments/a1/cancel"}, {"GET", "/v1/usage"}, {"GET", "/v1/doctors"}}
	for _, key := range []string{"", "wrong"} {
		for _, r := range routes {
			rec := call(mux, r[0], r[1], bookBody, map[string]string{"X-API-Key": key})
			if rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" {
				t.Fatalf("%s %s key=%q: %d %s", r[0], r[1], key, rec.Code, rec.Body)
			}
		}
	}
}

func TestPartnerRateLimitReturns429WithRetryAfter(t *testing.T) {
	mux := partnerMux(&fakePartner{}, 1)
	call(mux, "GET", "/v1/doctors", "", map[string]string{"X-API-Key": "secret-1"})
	rec := call(mux, "GET", "/v1/doctors", "", map[string]string{"X-API-Key": "secret-1"})
	if rec.Code != 429 || errorCode(t, rec) != "RATE_LIMITED" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d %s retry=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
}

func TestPartnerBookReturns201AndMarksReplays(t *testing.T) {
	for _, replayed := range []bool{false, true} {
		f := &fakePartner{replayed: replayed}
		rec := call(partnerMux(f, 0), "POST", "/v1/appointments", bookBody, bookHeaders())
		if rec.Code != 201 || rec.Header().Get("Location") != "/v1/appointments/a1" {
			t.Fatalf("replayed=%v: %d %s location=%q", replayed, rec.Code, rec.Body, rec.Header().Get("Location"))
		}
		want := "false"
		if replayed {
			want = "true"
		}
		if rec.Header().Get("Idempotency-Replayed") != want || f.consumer != "carcruds" || f.key != "chequeo-001" {
			t.Fatalf("replayed header=%q consumer=%q key=%q", rec.Header().Get("Idempotency-Replayed"), f.consumer, f.key)
		}
		var dto map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &dto)
		for _, field := range []string{"id", "patientId", "doctorId", "startAt", "endAt", "status", "version"} {
			if _, ok := dto[field]; !ok {
				t.Fatalf("missing %s in %s", field, rec.Body)
			}
		}
	}
}

func TestPartnerBookValidatesHeadersAndBody(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		body    string
		status  int
		errCode string
	}{
		{"missing key", func(h map[string]string) { delete(h, "Idempotency-Key") }, bookBody, 400, "INVALID_IDEMPOTENCY_KEY"},
		{"bad key", func(h map[string]string) { h["Idempotency-Key"] = "con espacios" }, bookBody, 400, "INVALID_IDEMPOTENCY_KEY"},
		{"content type", func(h map[string]string) { h["Content-Type"] = "text/plain" }, bookBody, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"unknown field", func(map[string]string) {}, `{"patientId":"p","doctorId":"d","startAt":"2026-12-01T10:00:00Z","x":1}`, 400, "INVALID_JSON"},
		{"bad date", func(map[string]string) {}, `{"patientId":"p","doctorId":"d","startAt":"mañana"}`, 400, "INVALID_REQUEST"},
	}
	for _, c := range cases {
		h := bookHeaders()
		c.mutate(h)
		rec := call(partnerMux(&fakePartner{}, 0), "POST", "/v1/appointments", c.body, h)
		if rec.Code != c.status || errorCode(t, rec) != c.errCode {
			t.Fatalf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
}

// Los codigos que el cliente de Car Cruds ya interpreta no deben cambiar.
func TestPartnerBookKeepsBusinessErrorCodes(t *testing.T) {
	cases := []struct {
		err     error
		status  int
		errCode string
	}{
		{domain.ErrLeadTime, 422, "LEAD_TIME_TOO_SHORT"},
		{domain.ErrOverlap, 409, "SLOT_UNAVAILABLE"},
		{domain.ErrPatientNotActive, 403, "PATIENT_NOT_ENABLED"},
		{app.ErrUnknownDoctor, 404, "UNKNOWN_DOCTOR"},
		{app.ErrUnknownPatient, 404, "UNKNOWN_PATIENT"},
		{app.ErrIdempotencyConflict, 409, "IDEMPOTENCY_CONFLICT"},
		{context.DeadlineExceeded, 503, "UNAVAILABLE"},
	}
	for _, c := range cases {
		rec := call(partnerMux(&fakePartner{err: c.err}, 0), "POST", "/v1/appointments", bookBody, bookHeaders())
		if rec.Code != c.status || errorCode(t, rec) != c.errCode {
			t.Fatalf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
	}
}

func TestPartnerGetAndCancelUseTheCallerAsOwner(t *testing.T) {
	f := &fakePartner{}
	mux := partnerMux(f, 0)
	rec := call(mux, "GET", "/v1/appointments/a1", "", map[string]string{"X-API-Key": "secret-1"})
	if rec.Code != 200 || f.consumer != "carcruds" {
		t.Fatalf("get: %d consumer=%q", rec.Code, f.consumer)
	}
	rec = call(mux, "POST", "/v1/appointments/a1/cancel", "", map[string]string{"X-API-Key": "secret-1"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	f.err = app.ErrNotFound
	rec = call(mux, "GET", "/v1/appointments/ajeno", "", map[string]string{"X-API-Key": "secret-1"})
	if rec.Code != 404 || errorCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("foreign: %d %s", rec.Code, rec.Body)
	}
}

func TestPartnerUsageReportsPeriodTotalsAndPrice(t *testing.T) {
	f := &fakePartner{}
	mux := partnerMux(f, 0)
	rec := call(mux, "GET", "/v1/usage?period=2026-09", "", map[string]string{"X-API-Key": "secret-1"})
	var got struct {
		Consumer     string `json:"consumer"`
		Period       string `json:"period"`
		CurrentPrice struct {
			AmountCents int64  `json:"amountCents"`
			Currency    string `json:"currency"`
		} `json:"currentPrice"`
		Totals []struct {
			Currency    string `json:"currency"`
			Bookings    int    `json:"bookings"`
			AmountCents int64  `json:"amountCents"`
		} `json:"totals"`
		Items []struct {
			AppointmentID string `json:"appointmentId"`
			Status        string `json:"status"`
		} `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Period != "2026-09" || got.Consumer != "carcruds" || got.CurrentPrice.AmountCents != 2500000 ||
		len(got.Totals) != 1 || got.Totals[0].AmountCents != 2500000 || len(got.Items) != 1 || got.Items[0].Status != "cancelled" {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}
	// Sin period se usa el mes actual (UTC).
	call(mux, "GET", "/v1/usage", "", map[string]string{"X-API-Key": "secret-1"})
	if f.month.Format("2006-01") != "2026-10" {
		t.Fatalf("default month = %s", f.month)
	}
	for _, bad := range []string{"2026-13", "oct", "2026-1"} {
		rec = call(mux, "GET", "/v1/usage?period="+bad, "", map[string]string{"X-API-Key": "secret-1"})
		if rec.Code != 400 || errorCode(t, rec) != "INVALID_PERIOD" {
			t.Fatalf("period %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
}

func TestPartnerDoctorsListsIDsAndDuration(t *testing.T) {
	rec := call(partnerMux(&fakePartner{}, 0), "GET", "/v1/doctors", "", map[string]string{"X-API-Key": "secret-1"})
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"doctors":[{"id":"doc-001","durationMinutes":30}]}` {
		t.Fatalf("doctors: %d %s", rec.Code, rec.Body)
	}
}

func TestPartnerWrongMethodIs405(t *testing.T) {
	rec := call(partnerMux(&fakePartner{}, 0), "DELETE", "/v1/appointments/a1", "", map[string]string{"X-API-Key": "secret-1"})
	if rec.Code != 405 || errorCode(t, rec) != "METHOD_NOT_ALLOWED" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
