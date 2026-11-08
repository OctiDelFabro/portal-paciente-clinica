package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

type fakeUC struct {
	searchErr  error
	lastQuery  domain.Query
	updated    int
	reindexErr error
}

func (f *fakeUC) UpdateProfile(_ context.Context, id string, p domain.Profile) (domain.Doctor, error) {
	if id != "doc-001" {
		return domain.Doctor{}, app.ErrNotFound
	}
	if err := p.Validate(); err != nil {
		return domain.Doctor{}, err
	}
	f.updated++
	return domain.Doctor{ID: id, FullName: p.FullName, Specialties: p.Specialties, Active: p.Active, ProfileVersion: 2}, nil
}
func (f *fakeUC) Doctor(_ context.Context, id string) (domain.Doctor, error) {
	if id != "doc-001" {
		return domain.Doctor{}, app.ErrNotFound
	}
	return domain.Doctor{ID: id, FullName: "Laura Gomez", Specialties: []string{"cardiologia"}, Active: true, ProfileVersion: 1}, nil
}
func (f *fakeUC) Specialties(context.Context) ([]domain.Specialty, error) {
	return []domain.Specialty{{Code: "cardiologia", Name: "Cardiologia"}}, nil
}
func (f *fakeUC) Search(_ context.Context, q domain.Query) (domain.Page, error) {
	if err := q.Validate(); err != nil {
		return domain.Page{}, err
	}
	f.lastQuery = q
	next := time.Date(2026, 12, 2, 10, 0, 0, 0, time.UTC)
	return domain.Page{Total: 1, Items: []domain.SearchDoc{{DoctorID: "doc-001", FullName: "Laura Gomez", Specialties: []string{"cardiologia"}, Active: true, NextAvailableAt: &next}}}, f.searchErr
}
func (f *fakeUC) Reindex(context.Context) (string, int, error) { return "doctors-v2", 2, f.reindexErr }

func serve(uc *fakeUC, method, path, role, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Register(mux, uc)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req.Header.Set(RoleHeader, role)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

const profileBody = `{"fullName":"Laura B. Gomez","specialties":["cardiologia"],"acceptsMinors":false,"active":true}`

func TestProtectedEndpointsRequireAdminRole(t *testing.T) {
	for _, tt := range []struct{ method, path, body string }{
		{"PUT", "/doctors/doc-001", profileBody},
		{"POST", "/admin/reindex", ""},
	} {
		for _, role := range []string{"", "patient"} {
			uc := &fakeUC{}
			rec := serve(uc, tt.method, tt.path, role, tt.body)
			if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"code":"FORBIDDEN"`) || uc.updated != 0 {
				t.Fatalf("%s %s role=%q: want 403 FORBIDDEN, got %d %s", tt.method, tt.path, role, rec.Code, rec.Body)
			}
		}
	}
}

func TestUpdateProfile(t *testing.T) {
	tests := []struct {
		name, path, body, want string
		code                   int
	}{
		{"saved", "/doctors/doc-001", profileBody, `"profileVersion":2`, 200},
		{"unknown doctor", "/doctors/doc-999", profileBody, `"code":"NOT_FOUND"`, 404},
		{"invalid profile", "/doctors/doc-001", `{"fullName":"","specialties":[],"acceptsMinors":false,"active":true}`, `"code":"VALIDATION_ERROR"`, 422},
		{"missing field", "/doctors/doc-001", `{"fullName":"X","specialties":["a"]}`, `"code":"INVALID_REQUEST"`, 400},
		{"unknown field", "/doctors/doc-001", `{"fullName":"X","specialties":["a"],"acceptsMinors":true,"active":true,"x":1}`, `"code":"INVALID_JSON"`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(&fakeUC{}, "PUT", tt.path, "admin", tt.body)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("want %d %s, got %d %s", tt.code, tt.want, rec.Code, rec.Body)
			}
		})
	}
}

func TestSearchParametersAndLimits(t *testing.T) {
	tests := []struct {
		name, query string
		code        int
	}{
		{"defaults", "", 200},
		{"all filters", "?q=gomez&specialty=cardiologia&acceptsMinors=true&sort=nextAvailable&page=2&size=50", 200},
		{"size over 50", "?size=51", 422},
		{"window over 1000", "?page=21&size=50", 422},
		{"bad sort", "?sort=price", 422},
		{"bad boolean", "?acceptsMinors=maybe", 422},
		{"bad page", "?page=abc", 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(&fakeUC{}, "GET", "/search"+tt.query, "", "")
			if rec.Code != tt.code {
				t.Fatalf("want %d, got %d %s", tt.code, rec.Code, rec.Body)
			}
		})
	}
	uc := &fakeUC{}
	rec := serve(uc, "GET", "/search?q=gomez&specialty=cardiologia&acceptsMinors=true&sort=nextAvailable&page=2&size=5", "", "")
	q := uc.lastQuery
	if q.Q != "gomez" || q.Specialty != "cardiologia" || q.AcceptsMinors == nil || !*q.AcceptsMinors || q.Sort != "nextAvailable" || q.Page != 2 || q.Size != 5 {
		t.Fatalf("query not parsed: %+v", q)
	}
	want := `{"items":[{"id":"doc-001","fullName":"Laura Gomez","specialties":["cardiologia"],"acceptsMinors":false,"nextAvailableAt":"2026-12-02T10:00:00Z"}],"page":2,"size":5,"total":1}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestSearchUnavailableReturns503(t *testing.T) {
	rec := serve(&fakeUC{searchErr: app.ErrSearchUnavailable}, "GET", "/search?q=x", "", "")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"code":"SEARCH_UNAVAILABLE"`) {
		t.Fatalf("want 503 SEARCH_UNAVAILABLE, got %d %s", rec.Code, rec.Body)
	}
	rec = serve(&fakeUC{reindexErr: app.ErrSearchUnavailable}, "POST", "/admin/reindex", "admin", "")
	if rec.Code != 503 {
		t.Fatalf("reindex with search down: want 503, got %d", rec.Code)
	}
	// Reads from MySQL keep working while OpenSearch is down.
	if rec := serve(&fakeUC{searchErr: app.ErrSearchUnavailable}, "GET", "/doctors/doc-001", "", ""); rec.Code != 200 {
		t.Fatalf("doctor read must not depend on search, got %d", rec.Code)
	}
}

func TestReads(t *testing.T) {
	tests := []struct {
		name, path, want string
		code             int
	}{
		{"doctor", "/doctors/doc-001", `{"id":"doc-001","fullName":"Laura Gomez","specialties":["cardiologia"],"acceptsMinors":false,"active":true,"profileVersion":1}`, 200},
		{"unknown doctor", "/doctors/doc-999", `{"error":{"code":"NOT_FOUND","message":"Doctor not found."}}`, 404},
		{"specialties", "/specialties", `{"specialties":[{"code":"cardiologia","name":"Cardiologia"}]}`, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(&fakeUC{}, "GET", tt.path, "", "")
			if rec.Code != tt.code || strings.TrimSpace(rec.Body.String()) != tt.want {
				t.Fatalf("want %d %s, got %d %s", tt.code, tt.want, rec.Code, rec.Body)
			}
		})
	}
	rec := serve(&fakeUC{}, "POST", "/admin/reindex", "admin", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"index":"doctors-v2","documents":2}` {
		t.Fatalf("reindex: %d %s", rec.Code, rec.Body)
	}
}
