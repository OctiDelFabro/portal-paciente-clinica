package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve sends one request with optional X-Patient-Id to the portal routes.
func serve(uc UseCases, method, path, body, patient string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Register(mux, uc)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if patient != "" {
		req.Header.Set(PatientHeader, patient)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestBookWithPatientHeaderUsesTheAuthenticatedPatient(t *testing.T) {
	for _, body := range []string{
		`{"doctorId":"doc-001","startAt":"2026-10-25T10:00:00-03:00"}`,
		`{"patientId":"pat-900","doctorId":"doc-001","startAt":"2026-10-25T10:00:00-03:00"}`,
	} {
		var calls []string
		rec := serve(fakeUC{calls: &calls}, "POST", "/appointments", body, "pat-900")
		if rec.Code != 201 || len(calls) != 1 || calls[0] != "BookForPatient:pat-900" {
			t.Errorf("%s: got %d %v %s", body, rec.Code, calls, rec.Body)
		}
	}
}

func TestBookWithPatientHeaderRejectsAnotherPatient(t *testing.T) {
	var calls []string
	rec := serve(fakeUC{calls: &calls}, "POST", "/appointments", valid, "pat-900")
	if rec.Code != 403 || errorCode(t, rec) != "PATIENT_MISMATCH" || len(calls) != 0 {
		t.Fatalf("want 403 PATIENT_MISMATCH without booking, got %d %v %s", rec.Code, calls, rec.Body)
	}
}

// Previous behavior: without the header the body patient is booked through the directory.
func TestBookWithoutPatientHeaderKeepsPreviousBehavior(t *testing.T) {
	var calls []string
	rec := serve(fakeUC{calls: &calls}, "POST", "/appointments", valid, "")
	if rec.Code != 201 || len(calls) != 1 || calls[0] != "Book:pat-001" {
		t.Fatalf("got %d %v", rec.Code, calls)
	}
	if rec := serve(fakeUC{}, "POST", "/appointments", `{"doctorId":"doc-001","startAt":"2026-10-25T10:00:00-03:00"}`, ""); rec.Code != 400 {
		t.Fatalf("missing patientId without header: want 400, got %d", rec.Code)
	}
}

func TestMalformedPatientHeaderIsUnauthorized(t *testing.T) {
	rec := serve(fakeUC{}, "POST", "/appointments", valid, "bad id with spaces")
	if rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestListAppointmentsRequiresThePatientHeader(t *testing.T) {
	if rec := serve(fakeUC{}, "GET", "/appointments", "", ""); rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" {
		t.Fatalf("without header: got %d %s", rec.Code, rec.Body)
	}
	var calls []string
	rec := serve(fakeUC{calls: &calls}, "GET", "/appointments", "", "pat-900")
	var body struct {
		Appointments []appointmentDTO `json:"appointments"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || len(calls) != 1 || calls[0] != "ListForPatient:pat-900" || len(body.Appointments) != 1 || body.Appointments[0].PatientID != "pat-900" {
		t.Fatalf("got %d %v %s", rec.Code, calls, rec.Body)
	}
}

func TestGetAndCancelScopeToThePatientHeader(t *testing.T) {
	cases := []struct {
		method, path, patient, want string
	}{
		{"GET", "/appointments/a-1", "pat-900", "GetForPatient:pat-900"},
		{"POST", "/appointments/a-1/cancel", "pat-900", "CancelForPatient:pat-900"},
		// Previous behavior without the header.
		{"GET", "/appointments/a-1", "", "Get"},
		{"POST", "/appointments/a-1/cancel", "", "Cancel"},
	}
	for _, c := range cases {
		var calls []string
		rec := serve(fakeUC{calls: &calls}, c.method, c.path, "", c.patient)
		if rec.Code != 200 || len(calls) != 1 || calls[0] != c.want {
			t.Errorf("%s %s patient=%q: got %d %v", c.method, c.path, c.patient, rec.Code, calls)
		}
	}
}

func TestPartnerBookingIgnoresThePatientHeader(t *testing.T) {
	f := &fakePartner{}
	req := httptest.NewRequest("POST", "/v1/appointments", strings.NewReader(`{"patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-01T10:00:00Z"}`))
	req.Header.Set("X-API-Key", "secret-1")
	req.Header.Set("Idempotency-Key", "k-1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(PatientHeader, "pat-900")
	rec := httptest.NewRecorder()
	partnerMux(f, 60).ServeHTTP(rec, req)
	if rec.Code != 201 || f.consumer != "carcruds" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
