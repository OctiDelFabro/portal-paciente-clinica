package service

import (
	"context"
	"portal-paciente-clinica/services/pacientes/internal/repository"
	"testing"
	"time"
)

type dependentStore struct{ *fakeStore }

func (f dependentStore) Dependents(_ context.Context, id string) ([]repository.Patient, error) {
	out := []repository.Patient{}
	for _, p := range f.byID {
		if p.ResponsibleID == id {
			out = append(out, p)
		}
	}
	return out, nil
}
func TestDependentsRecheckVerificationAndCivilAge(t *testing.T) {
	base := newFakeStore()
	s, _ := newTestService(t, base)
	s.Store = dependentStore{base}
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 2, 59, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	base.byID["parent"] = repository.Patient{ID: "parent", Role: "patient", Enabled: true, BirthDate: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)}
	base.byID["child"] = repository.Patient{ID: "child", Role: "patient", Enabled: true, ResponsibleID: "parent", BirthDate: time.Date(2008, 11, 11, 0, 0, 0, 0, time.UTC)}
	deps, err := s.Dependents(ctx, "parent")
	if err != nil || len(deps) != 1 {
		t.Fatalf("became adult before clinic birthday: %+v %v", deps, err)
	}
	if !s.CanReadContact(ctx, "parent", "child", "patient") || s.CanReadContact(ctx, "other", "child", "patient") {
		t.Fatal("contact scope")
	}
	now = now.Add(time.Minute)
	deps, err = s.Dependents(ctx, "parent")
	if err != nil || len(deps) != 0 {
		t.Fatal("adult remained a minor")
	}
	p := base.byID["parent"]
	p.Enabled = false
	base.byID[p.ID] = p
	if _, err = s.Dependents(ctx, "parent"); err == nil {
		t.Fatal("revoked adult retained access")
	}
}
