package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

type call struct{ method, path, query, body string }

// engine records requests and answers with the handler's response.
type engine struct {
	mu     sync.Mutex
	calls  []call
	answer func(c call) (int, string)
}

func (e *engine) start(t *testing.T) *Index {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := call{r.Method, r.URL.Path, r.URL.RawQuery, string(b)}
		e.mu.Lock()
		e.calls = append(e.calls, c)
		e.mu.Unlock()
		code, body := e.answer(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestSearchBuildsFilteredSortedPagedQuery(t *testing.T) {
	e := &engine{answer: func(call) (int, string) {
		return 200, `{"hits":{"total":{"value":7},"hits":[{"_source":{"doctorId":"doc-002","fullName":"Martin Pereyra","specialties":["pediatria"],"acceptsMinors":true,"active":true,"nextAvailableAt":"2026-12-02T10:00:00Z","profileVersion":3}}]}}`
	}}
	ix := e.start(t)
	yes := true
	page, err := ix.Search(context.Background(), domain.Query{Q: "pereyra", Specialty: "pediatria", AcceptsMinors: &yes, Sort: domain.SortNextAvailable, Page: 3, Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 7 || len(page.Items) != 1 || page.Items[0].DoctorID != "doc-002" || page.Items[0].NextAvailableAt == nil || page.Items[0].ProfileVersion != 3 {
		t.Fatalf("unexpected page %+v", page)
	}
	c := e.calls[0]
	if c.method != "POST" || c.path != "/doctors/_search" {
		t.Fatalf("searched %s %s, want the alias", c.method, c.path)
	}
	var body struct {
		From  int `json:"from"`
		Size  int `json:"size"`
		Query struct {
			Bool struct {
				Filter []map[string]map[string]any `json:"filter"`
				Must   []map[string]any            `json:"must"`
			} `json:"bool"`
		} `json:"query"`
		Sort []map[string]any `json:"sort"`
	}
	if err := json.Unmarshal([]byte(c.body), &body); err != nil {
		t.Fatal(err)
	}
	if body.From != 10 || body.Size != 5 {
		t.Fatalf("from/size = %d/%d", body.From, body.Size)
	}
	filters := map[string]any{}
	for _, f := range body.Query.Bool.Filter {
		for field, v := range f["term"] {
			filters[field] = v
		}
	}
	if filters["active"] != true || filters["specialties"] != "pediatria" || filters["acceptsMinors"] != true || len(body.Query.Bool.Must) != 1 {
		t.Fatalf("filters %v must %v", filters, body.Query.Bool.Must)
	}
	if _, ok := body.Sort[0]["nextAvailableAt"]; !ok {
		t.Fatalf("sort %v must start by nextAvailableAt", body.Sort)
	}
}

func TestSortOrders(t *testing.T) {
	tests := []struct{ sort, q, first string }{
		{domain.SortName, "", "fullName.keyword"},
		{domain.SortRelevant, "", "fullName.keyword"},
		{domain.SortRelevant, "gomez", "_score"},
	}
	for _, tt := range tests {
		e := &engine{answer: func(call) (int, string) { return 200, `{"hits":{"total":{"value":0},"hits":[]}}` }}
		if _, err := e.start(t).Search(context.Background(), domain.Query{Q: tt.q, Sort: tt.sort, Page: 1, Size: 10}); err != nil {
			t.Fatal(err)
		}
		var body struct{ Sort []map[string]any }
		_ = json.Unmarshal([]byte(e.calls[0].body), &body)
		if _, ok := body.Sort[0][tt.first]; !ok {
			t.Fatalf("sort=%q q=%q: want first %s, got %v", tt.sort, tt.q, tt.first, body.Sort)
		}
	}
}

func TestEngineDownIsSearchUnavailable(t *testing.T) {
	e := &engine{answer: func(call) (int, string) { return 503, `{"error":"cluster_block"}` }}
	if _, err := e.start(t).Search(context.Background(), domain.Query{Page: 1, Size: 10}); !errors.Is(err, app.ErrSearchUnavailable) {
		t.Fatalf("5xx: want ErrSearchUnavailable, got %v", err)
	}
	closed := New("http://127.0.0.1:1")
	if _, err := closed.Search(context.Background(), domain.Query{Page: 1, Size: 10}); !errors.Is(err, app.ErrSearchUnavailable) {
		t.Fatalf("connection refused: want ErrSearchUnavailable, got %v", err)
	}
	missing := &engine{answer: func(call) (int, string) { return 404, `{"error":{"type":"index_not_found_exception"}}` }}
	if _, err := missing.start(t).Search(context.Background(), domain.Query{Page: 1, Size: 10}); !errors.Is(err, app.ErrSearchUnavailable) {
		t.Fatalf("alias missing: want ErrSearchUnavailable, got %v", err)
	}
}

func TestUpsertUsesExternalVersioningThroughTheAlias(t *testing.T) {
	code := 201
	e := &engine{answer: func(call) (int, string) { return code, `{}` }}
	ix := e.start(t)
	next := time.Date(2026, 12, 2, 10, 0, 0, 0, time.UTC)
	doc := domain.SearchDoc{DoctorID: "doc-001", FullName: "Laura Gomez", Specialties: []string{"cardiologia"}, Active: true, NextAvailableAt: &next, ProfileVersion: 4}
	if err := ix.Upsert(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	c := e.calls[0]
	if c.method != "PUT" || c.path != "/doctors/_doc/doc-001" || !strings.Contains(c.query, "version=4") ||
		!strings.Contains(c.query, "version_type=external_gte") || !strings.Contains(c.query, "require_alias=true") {
		t.Fatalf("unexpected upsert %s %s?%s", c.method, c.path, c.query)
	}
	code = 409 // an older profileVersion than the indexed one: newer data wins, not an error
	if err := ix.Upsert(context.Background(), doc); err != nil {
		t.Fatalf("version conflict must be ignored, got %v", err)
	}
	code = 404 // alias not created yet: must fail so the event is retried
	if err := ix.Upsert(context.Background(), doc); err == nil {
		t.Fatal("missing alias must be an error")
	}
}

func TestRebuildCreatesNextVersionAndSwapsAliasAtomically(t *testing.T) {
	e := &engine{answer: func(c call) (int, string) {
		if c.method == "GET" && c.path == "/doctors-v*/_alias" {
			return 200, `{"doctors-v1":{"aliases":{}},"doctors-v2":{"aliases":{"doctors":{}}}}`
		}
		if strings.HasSuffix(c.path, "/_bulk") {
			return 200, `{"errors":false,"items":[]}`
		}
		return 200, `{"acknowledged":true}`
	}}
	ix := e.start(t)
	docs := []domain.SearchDoc{{DoctorID: "doc-001", FullName: "A", ProfileVersion: 1}, {DoctorID: "doc-002", FullName: "B", ProfileVersion: 2}}
	name, err := ix.Rebuild(context.Background(), docs)
	if err != nil || name != "doctors-v3" {
		t.Fatalf("name=%s err=%v", name, err)
	}
	var created, bulk, aliases bool
	for _, c := range e.calls {
		switch {
		case c.method == "PUT" && c.path == "/doctors-v3":
			created = strings.Contains(c.body, `"profileVersion":{"type":"long"}`)
		case c.method == "POST" && c.path == "/doctors-v3/_bulk":
			bulk = strings.Count(c.body, `"_id":"doc-00`) == 2 && strings.Contains(c.body, `"version_type":"external_gte"`)
		case c.method == "POST" && c.path == "/_aliases":
			aliases = c.body == `{"actions":[{"remove":{"alias":"doctors","index":"doctors-v2"}},{"add":{"alias":"doctors","index":"doctors-v3"}}]}`
		}
	}
	if !created || !bulk || !aliases {
		t.Fatalf("created=%v bulk=%v aliases=%v calls=%+v", created, bulk, aliases, e.calls)
	}
}

func TestRebuildFromScratchCreatesV1(t *testing.T) {
	e := &engine{answer: func(c call) (int, string) {
		if c.method == "GET" {
			return 200, `{}`
		}
		if strings.HasSuffix(c.path, "/_bulk") {
			return 200, `{"errors":false}`
		}
		return 200, `{"acknowledged":true}`
	}}
	name, err := e.start(t).Rebuild(context.Background(), nil)
	if err != nil || name != "doctors-v1" {
		t.Fatalf("name=%s err=%v", name, err)
	}
	for _, c := range e.calls {
		if c.path == "/_aliases" && c.body != `{"actions":[{"add":{"alias":"doctors","index":"doctors-v1"}}]}` {
			t.Fatalf("aliases body %s", c.body)
		}
	}
}

func TestRebuildFailsOnBulkErrors(t *testing.T) {
	e := &engine{answer: func(c call) (int, string) {
		if c.method == "GET" {
			return 200, `{}`
		}
		if strings.HasSuffix(c.path, "/_bulk") {
			return 200, `{"errors":true}`
		}
		return 200, `{"acknowledged":true}`
	}}
	ix := e.start(t)
	if _, err := ix.Rebuild(context.Background(), []domain.SearchDoc{{DoctorID: "doc-001"}}); err == nil {
		t.Fatal("bulk errors must abort the rebuild")
	}
	for _, c := range e.calls {
		if c.path == "/_aliases" {
			t.Fatal("alias must not move to a partial index")
		}
	}
}
