package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

const bookedBody = `{"eventId":"0b8f7c3a-1111-4222-8333-444455556666","eventType":"turnos.reservado.v1","version":1,"occurredAt":"2026-10-20T12:00:00Z","correlationId":"c-1","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-10-25T10:00:00Z","endAt":"2026-10-25T10:30:00Z","appointmentVersion":1}}`

type fakeStore struct {
	applied   int
	duplicate bool
	applyErr  error
	noteErr   error
	notes     []service.Note
	timeline  map[string][]service.Encounter
	pingErr   error
}

func (f *fakeStore) Apply(context.Context, service.Change) (bool, error) {
	f.applied++
	return f.duplicate, f.applyErr
}
func (f *fakeStore) AddNote(_ context.Context, _ string, n service.Note) error {
	if f.noteErr != nil {
		return f.noteErr
	}
	f.notes = append(f.notes, n)
	return nil
}
func (f *fakeStore) Encounter(_ context.Context, id string) (service.Encounter, error) {
	if f.noteErr != nil {
		return service.Encounter{}, f.noteErr
	}
	if f.timeline != nil {
		for _, encs := range f.timeline {
			for _, enc := range encs {
				if enc.AppointmentID == id {
					return enc, nil
				}
			}
		}
	}
	return service.Encounter{AppointmentID: id, DoctorID: "doc-001", Status: service.StatusScheduled}, nil
}
func (f *fakeStore) Timeline(_ context.Context, patientID string) ([]service.Encounter, error) {
	return f.timeline[patientID], nil
}
func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

func newMux(store *fakeStore) *http.ServeMux {
	svc := &service.Service{Store: store, Now: func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) }, NewID: func() string { return "n-1" }}
	mux := http.NewServeMux()
	(&HTTP{Service: svc}).Register(mux)
	return mux
}

func call(mux *http.ServeMux, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Message == "" {
		t.Fatalf("not an error envelope: %s", rec.Body)
	}
	return body.Error.Code
}

func TestGetMyRecordsRequiresPatientIdentity(t *testing.T) {
	rec := call(newMux(&fakeStore{}), "GET", "/records/me", "", nil)
	if rec.Code != 401 || errorCode(t, rec) != "UNAUTHORIZED" {
		t.Fatalf("want 401 UNAUTHORIZED, got %d %s", rec.Code, rec.Body)
	}
}

func TestGetMyRecordsReturnsOnlyTheCallersTimeline(t *testing.T) {
	store := &fakeStore{timeline: map[string][]service.Encounter{
		"pat-001": {{AppointmentID: "t-2", PatientID: "pat-001", Status: "scheduled", Notes: []service.Note{{ID: "n-9", Text: "ok"}}}},
		"pat-002": {{AppointmentID: "t-3", PatientID: "pat-002"}},
	}}
	rec := call(newMux(store), "GET", "/records/me", "", map[string]string{"X-Patient-Id": "pat-001", "X-User-Role": "patient"})
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var body struct {
		PatientID string              `json:"patientId"`
		Records   []service.Encounter `json:"records"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.PatientID != "pat-001" || len(body.Records) != 1 || body.Records[0].AppointmentID != "t-2" || len(body.Records[0].Notes) != 1 {
		t.Fatalf("unexpected body %s", rec.Body)
	}
}

func TestGetMyRecordsEmptyTimelineIsAnEmptyList(t *testing.T) {
	rec := call(newMux(&fakeStore{}), "GET", "/records/me", "", map[string]string{"X-Patient-Id": "pat-404"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"records":[]`) {
		t.Fatalf("want empty list, got %d %s", rec.Code, rec.Body)
	}
}

func TestPostNoteAuthorization(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		status  int
		code    string
	}{
		{"no identity", map[string]string{}, 401, "UNAUTHORIZED"},
		{"patient cannot write", map[string]string{"X-Patient-Id": "pat-001", "X-User-Role": "patient"}, 403, "FORBIDDEN"},
		{"receptionist cannot write", map[string]string{"X-Patient-Id": "rec-1", "X-User-Role": "receptionist"}, 403, "FORBIDDEN"},
		{"unassigned doctor cannot write", map[string]string{"X-Patient-Id": "doc-999", "X-User-Role": "doctor"}, 403, "FORBIDDEN"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			tt.headers["Content-Type"] = "application/json"
			rec := call(newMux(store), "POST", "/records/t-1/notes", `{"text":"hola"}`, tt.headers)
			if rec.Code != tt.status || errorCode(t, rec) != tt.code || len(store.notes) != 0 {
				t.Fatalf("want %d %s and no note, got %d %s", tt.status, tt.code, rec.Code, rec.Body)
			}
		})
	}
}

var doctor = map[string]string{"X-Patient-Id": "doc-001", "X-User-Role": "doctor", "Content-Type": "application/json"}

func TestPostNoteByDoctorIsStored(t *testing.T) {
	store := &fakeStore{}
	rec := call(newMux(store), "POST", "/records/t-1/notes", `{"text":"Control en 30 dias"}`, doctor)
	if rec.Code != 201 || len(store.notes) != 1 || !strings.Contains(rec.Body.String(), `"text":"Control en 30 dias"`) {
		t.Fatalf("want 201 with note, got %d %s", rec.Code, rec.Body)
	}
}

func TestPostNoteRejections(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		err    error
		status int
		code   string
	}{
		{"invalid json", `{"text":`, nil, 400, "INVALID_JSON"},
		{"unknown field", `{"text":"a","x":1}`, nil, 400, "INVALID_JSON"},
		{"too long", `{"text":"` + strings.Repeat("a", 4001) + `"}`, nil, 422, "VALIDATION_ERROR"},
		{"empty", `{"text":""}`, nil, 422, "VALIDATION_ERROR"},
		{"unknown appointment", `{"text":"a"}`, service.ErrNotFound, 404, "NOT_FOUND"},
		{"cancelled appointment", `{"text":"a"}`, service.ErrCancelled, 409, "ENCOUNTER_CANCELLED"},
		{"storage down", `{"text":"a"}`, errors.New("mongo down"), 503, "SERVICE_UNAVAILABLE"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := call(newMux(&fakeStore{noteErr: tt.err}), "POST", "/records/t-1/notes", tt.body, doctor)
			if rec.Code != tt.status || errorCode(t, rec) != tt.code {
				t.Fatalf("want %d %s, got %d %s", tt.status, tt.code, rec.Code, rec.Body)
			}
		})
	}
}

func TestHealthzReportsMongoConnectivity(t *testing.T) {
	if rec := call(newMux(&fakeStore{}), "GET", "/healthz", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mongo":"up"`) {
		t.Fatalf("want 200 mongo up, got %d %s", rec.Code, rec.Body)
	}
	if rec := call(newMux(&fakeStore{pingErr: errors.New("down")}), "GET", "/healthz", "", nil); rec.Code != 503 || !strings.Contains(rec.Body.String(), `"mongo":"down"`) {
		t.Fatalf("want 503 mongo down, got %d %s", rec.Code, rec.Body)
	}
}

func TestConsumerDecision(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		store *fakeStore
		want  outcome
	}{
		{"processed", bookedBody, &fakeStore{}, outcomeOK},
		{"redelivered event", bookedBody, &fakeStore{duplicate: true}, outcomeDuplicate},
		{"bad json goes to dlq without touching storage", `{"broken":`, &fakeStore{}, outcomeDeadLetter},
		{"missing fields go to dlq", strings.Replace(bookedBody, `"patientId":"pat-001"`, `"patientId":""`, 1), &fakeStore{}, outcomeDeadLetter},
		{"transient mongo error retries", bookedBody, &fakeStore{applyErr: errors.New("timeout")}, outcomeRetry},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{Service: &service.Service{Store: tt.store, Now: time.Now, NewID: func() string { return "x" }}}
			got, _, _ := c.process(context.Background(), []byte(tt.body))
			if got != tt.want {
				t.Fatalf("want %v, got %v", tt.want, got)
			}
			if tt.want == outcomeDeadLetter && tt.store.applied != 0 {
				t.Fatal("unprocessable message reached storage")
			}
		})
	}
}

func TestRetryCountReadsBrokerHeaderTypes(t *testing.T) {
	for _, v := range []any{int32(2), int64(2), 2} {
		if n := retryCount(map[string]any{retryHeader: v}); n != 2 {
			t.Fatalf("%T: want 2, got %d", v, n)
		}
	}
	if retryCount(nil) != 0 {
		t.Fatal("missing header must count as 0")
	}
}
