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

// Portal routes fail closed without patient authentication.
func TestBookWithoutPatientHeaderIsUnauthorized(t *testing.T) {
	rec := serve(fakeUC{}, "POST", "/appointments", valid, "")
	if rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" {
		t.Fatalf("want 401 UNAUTHORIZED, got %d %s", rec.Code, rec.Body)
	}
}

func TestBookWithDoctorRoleIsForbidden(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, fakeUC{})
	req := httptest.NewRequest("POST", "/appointments", strings.NewReader(valid))
	req.Header.Set(PatientHeader, "doc-001")
	req.Header.Set("X-User-Role", "doctor")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 403 || errorCode(t, rec) != "FORBIDDEN" {
		t.Fatalf("want 403 FORBIDDEN, got %d %s", rec.Code, rec.Body)
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

func TestDoctorViewsAndCancelsAgenda(t *testing.T) {
	mux := http.NewServeMux()
	var calls []string
	Register(mux, fakeUC{calls: &calls})

	// GET /appointments with doctor role calls ListForDoctor
	req := httptest.NewRequest("GET", "/appointments", nil)
	req.Header.Set(PatientHeader, "doc-001")
	req.Header.Set("X-User-Role", "doctor")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || len(calls) != 1 || calls[0] != "ListForDoctor:doc-001" {
		t.Fatalf("doctor list: got %d %v", rec.Code, calls)
	}

	// GET /appointments/a-1 with doctor role calls GetForDoctor
	calls = nil
	req = httptest.NewRequest("GET", "/appointments/a-1", nil)
	req.Header.Set(PatientHeader, "doc-001")
	req.Header.Set("X-User-Role", "doctor")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || len(calls) != 1 || calls[0] != "GetForDoctor:doc-001" {
		t.Fatalf("doctor get: got %d %v", rec.Code, calls)
	}

	// POST /appointments/a-1/cancel with doctor role calls CancelForDoctor
	calls = nil
	req = httptest.NewRequest("POST", "/appointments/a-1/cancel", strings.NewReader(`{"reason":"doctor_reschedule"}`))
	req.Header.Set(PatientHeader, "doc-001")
	req.Header.Set("X-User-Role", "doctor")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || len(calls) != 1 || calls[0] != "CancelForDoctor:doc-001" {
		t.Fatalf("doctor cancel: got %d %v", rec.Code, calls)
	}
}

func TestGetAndCancelScopeToThePatientHeader(t *testing.T) {
	cases := []struct {
		method, path, patient, want string
		wantCode                    int
	}{
		{"GET", "/appointments/a-1", "pat-900", "GetForPatient:pat-900", 200},
		{"POST", "/appointments/a-1/cancel", "pat-900", "CancelForPatient:pat-900", 200},
		// Without header: fails closed with 401 UNAUTHORIZED
		{"GET", "/appointments/a-1", "", "", 401},
		{"POST", "/appointments/a-1/cancel", "", "", 401},
	}
	for _, c := range cases {
		var calls []string
		rec := serve(fakeUC{calls: &calls}, c.method, c.path, "", c.patient)
		if rec.Code != c.wantCode {
			t.Errorf("%s %s patient=%q: got %d want %d", c.method, c.path, c.patient, rec.Code, c.wantCode)
		}
		if c.want != "" && (len(calls) != 1 || calls[0] != c.want) {
			t.Errorf("%s %s patient=%q: got %v want %s", c.method, c.path, c.patient, calls, c.want)
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
