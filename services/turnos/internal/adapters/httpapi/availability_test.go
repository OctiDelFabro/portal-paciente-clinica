package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

type fakeAvailability struct {
	err    error
	doctor string
	day    time.Time
}

func (f *fakeAvailability) Availability(_ context.Context, doctorID string, day time.Time) (app.Availability, error) {
	f.doctor, f.day = doctorID, day
	start := day.Add(9 * time.Hour)
	return app.Availability{DoctorID: doctorID, Date: domain.DayOf(day), Duration: 30 * time.Minute,
		Slots: []domain.Slot{{StartAt: start, EndAt: start.Add(30 * time.Minute)}}}, f.err
}

func getAvailability(q app.AvailabilityQuery, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	RegisterAvailability(mux, q)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestAvailabilityReturnsFreeSlotsOfTheDay(t *testing.T) {
	f := &fakeAvailability{}
	rec := getAvailability(f, "/doctors/doc-001/availability?date=2026-11-12")
	want := `{"doctorId":"doc-001","date":"2026-11-12","durationMinutes":30,"slots":[{"startAt":"2026-11-12T09:00:00-03:00","endAt":"2026-11-12T09:30:00-03:00"}]}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if f.doctor != "doc-001" || domain.DayOf(f.day) != "2026-11-12" {
		t.Fatalf("query received %s %v", f.doctor, f.day)
	}
}

func TestAvailabilityRejectsMissingOrInvalidDate(t *testing.T) {
	for _, path := range []string{"/doctors/doc-001/availability", "/doctors/doc-001/availability?date=12-11-2026"} {
		if rec := getAvailability(&fakeAvailability{}, path); rec.Code != 400 || errorCode(t, rec) != "INVALID_DATE" {
			t.Errorf("%s: got %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestAvailabilityOfUnknownDoctorIs404(t *testing.T) {
	rec := getAvailability(&fakeAvailability{err: app.ErrUnknownDoctor}, "/doctors/nope/availability?date=2026-11-12")
	if rec.Code != 404 || errorCode(t, rec) != "UNKNOWN_DOCTOR" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestHealthReportsDatabaseState(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{{nil, 200}, {errors.New("dial tcp: connection refused"), 503}} {
		rec := httptest.NewRecorder()
		Health(func(context.Context) error { return c.err }).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
		if rec.Code != c.code {
			t.Errorf("ping err=%v: want %d, got %d", c.err, c.code, rec.Code)
		}
	}
}

func TestHealthTimesOutAHangingDatabase(t *testing.T) {
	rec := httptest.NewRecorder()
	start := time.Now()
	Health(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 503 || time.Since(start) > time.Second {
		t.Fatalf("want fast 503, got %d after %v", rec.Code, time.Since(start))
	}
}

func TestWithInstanceSetsResponseHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	WithInstance("turnos-2", http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Header().Get("X-Instance") != "turnos-2" {
		t.Fatalf("got %q", rec.Header().Get("X-Instance"))
	}
}
