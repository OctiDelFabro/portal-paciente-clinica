//go:build integration

// Integration tests against a real OpenSearch. Requires Docker:
// go test -tags integration ./services/medicos/...
package opensearch

import (
	"context"
	"testing"
	"time"

	tcopensearch "github.com/testcontainers/testcontainers-go/modules/opensearch"

	"portal-paciente-clinica/services/medicos/internal/domain"
)

func startIndex(t *testing.T) *Index {
	t.Helper()
	ctx := context.Background()
	c, err := tcopensearch.Run(ctx, "opensearchproject/opensearch:3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	addr, err := c.Address(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return New(addr)
}

func refresh(t *testing.T, ix *Index) {
	t.Helper()
	if _, _, err := ix.doJSON(context.Background(), "POST", "/"+Alias+"/_refresh", nil); err != nil {
		t.Fatal(err)
	}
}

func ids(p domain.Page) []string {
	out := []string{}
	for _, d := range p.Items {
		out = append(out, d.DoctorID)
	}
	return out
}

func TestSearchFiltersSortsAndPaginatesOnRealEngine(t *testing.T) {
	ctx := context.Background()
	ix := startIndex(t)
	early, late := time.Date(2026, 12, 2, 9, 0, 0, 0, time.UTC), time.Date(2026, 12, 5, 9, 0, 0, 0, time.UTC)
	docs := []domain.SearchDoc{
		{DoctorID: "doc-001", FullName: "Laura Gómez", Specialties: []string{"cardiologia"}, Active: true, NextAvailableAt: &late, ProfileVersion: 1},
		{DoctorID: "doc-002", FullName: "Martín Pereyra", Specialties: []string{"pediatria"}, AcceptsMinors: true, Active: true, NextAvailableAt: &early, ProfileVersion: 1},
		{DoctorID: "doc-003", FullName: "Ana Ibáñez", Specialties: []string{"pediatria"}, AcceptsMinors: true, Active: false, ProfileVersion: 1},
	}
	if name, err := ix.Rebuild(ctx, docs); err != nil || name != "doctors-v1" {
		t.Fatalf("rebuild: %s %v", name, err)
	}
	yes := true
	tests := []struct {
		name string
		q    domain.Query
		want []string
	}{
		{"inactive excluded, by name", domain.Query{Sort: domain.SortName, Page: 1, Size: 10}, []string{"doc-001", "doc-002"}},
		{"next available first", domain.Query{Sort: domain.SortNextAvailable, Page: 1, Size: 10}, []string{"doc-002", "doc-001"}},
		{"specialty filter", domain.Query{Specialty: "cardiologia", Page: 1, Size: 10}, []string{"doc-001"}},
		{"accepts minors", domain.Query{AcceptsMinors: &yes, Page: 1, Size: 10}, []string{"doc-002"}},
		{"specialty text with accent", domain.Query{Q: "pediatría", Page: 1, Size: 10}, []string{"doc-002"}},
		{"full text with typo", domain.Query{Q: "pereira", Page: 1, Size: 10}, []string{"doc-002"}},
		{"second page", domain.Query{Sort: domain.SortName, Page: 2, Size: 1}, []string{"doc-002"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ix.Search(ctx, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(p); len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
				t.Fatalf("want %v, got %v", tt.want, got)
			}
		})
	}
}

func TestExternalVersionRejectsStaleProfileAndRebuildSwapsAlias(t *testing.T) {
	ctx := context.Background()
	ix := startIndex(t)
	doc := domain.SearchDoc{DoctorID: "doc-001", FullName: "Laura Gómez", Specialties: []string{"cardiologia"}, Active: true, ProfileVersion: 1}
	if _, err := ix.Rebuild(ctx, []domain.SearchDoc{doc}); err != nil {
		t.Fatal(err)
	}
	newer := doc
	newer.FullName, newer.ProfileVersion = "Laura Beatriz Gómez", 3
	stale := doc
	stale.FullName, stale.ProfileVersion = "Laura Old", 2
	for _, d := range []domain.SearchDoc{newer, stale} {
		if err := ix.Upsert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, ix)
	p, _ := ix.Search(ctx, domain.Query{Page: 1, Size: 10})
	if len(p.Items) != 1 || p.Items[0].FullName != "Laura Beatriz Gómez" || p.Items[0].ProfileVersion != 3 {
		t.Fatalf("stale event overwrote newer profile: %+v", p.Items)
	}
	name, err := ix.Rebuild(ctx, []domain.SearchDoc{newer})
	if err != nil || name != "doctors-v2" {
		t.Fatalf("second rebuild: %s %v", name, err)
	}
	code, _, _ := ix.doJSON(ctx, "GET", "/doctors-v1", nil)
	if code != 404 {
		t.Fatalf("old index must be deleted after the swap, got %d", code)
	}
	if p, err := ix.Search(ctx, domain.Query{Page: 1, Size: 10}); err != nil || p.Total != 1 {
		t.Fatalf("search after swap: %+v %v", p, err)
	}
}
