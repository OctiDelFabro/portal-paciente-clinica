package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type fakeUC struct{ err error }

func (f fakeUC) Book(_ context.Context, p, d string, s time.Time) (domain.Appointment, error) {
	return domain.Appointment{ID: "a-1", PatientID: p, DoctorID: d, StartAt: s, EndAt: s.Add(30 * time.Minute), Status: domain.StatusBooked, Version: 1}, f.err
}
func (f fakeUC) Cancel(context.Context, string, string) (domain.Appointment, error) {
	return domain.Appointment{}, f.err
}
func (f fakeUC) Get(context.Context, string) (domain.Appointment, error) {
	return domain.Appointment{}, f.err
}

func post(t *testing.T, uc UseCases, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Register(mux, uc)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

const valid = `{"patientId":"pat-001","doctorId":"doc-001","startAt":"2026-10-25T10:00:00-03:00"}`

func TestBookReturns201WithLocation(t *testing.T) {
	rec := post(t, fakeUC{}, "/appointments", valid)
	if rec.Code != 201 || rec.Header().Get("Location") != "/appointments/a-1" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestBookMapsBusinessErrors(t *testing.T) {
	cases := map[error]struct {
		status int
		code   string
	}{
		domain.ErrLeadTime:         {422, "LEAD_TIME_TOO_SHORT"},
		domain.ErrOverlap:          {409, "SLOT_UNAVAILABLE"},
		domain.ErrPatientNotActive: {403, "PATIENT_NOT_ENABLED"},
		app.ErrUnknownDoctor:       {404, "UNKNOWN_DOCTOR"},
		context.DeadlineExceeded:   {503, "UNAVAILABLE"},
	}
	for err, want := range cases {
		rec := post(t, fakeUC{err: err}, "/appointments", valid)
		if rec.Code != want.status || errorCode(t, rec) != want.code {
			t.Errorf("%v: got %d %s", err, rec.Code, rec.Body)
		}
	}
}

func TestBookRejectsUnknownFieldsAndBadDates(t *testing.T) {
	for _, body := range []string{`{"patientId":"p","doctorId":"d","startAt":"mañana"}`, `{"extra":1}`} {
		if rec := post(t, fakeUC{}, "/appointments", body); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d", body, rec.Code)
		}
	}
}
