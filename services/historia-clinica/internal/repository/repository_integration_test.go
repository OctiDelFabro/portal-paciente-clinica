//go:build integration

// Integration tests against a real MongoDB (brief 2.11).
// Requires Docker: go test -tags integration ./services/historia-clinica/...
package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"

	"portal-paciente-clinica/services/historia-clinica/internal/repository"
	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

func startRepo(t *testing.T) *repository.Repo {
	t.Helper()
	ctx := context.Background()
	c, err := mongodb.Run(ctx, "mongo:8.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	uri, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.Open(ctx, uri, "historia_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close(ctx) })
	return repo
}

var start = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func change(eventID, appointmentID, status string, startAt, at time.Time) service.Change {
	c := service.Change{EventID: eventID, AppointmentID: appointmentID, PatientID: "pat-001", DoctorID: "doc-001",
		StartAt: startAt, Status: status, At: at}
	if status == service.StatusCancelled {
		c.CancelReason = "patient_request"
	}
	return c
}

func TestCancelBeforeBookStaysCancelled(t *testing.T) {
	repo := startRepo(t)
	ctx := context.Background()
	if _, err := repo.Apply(ctx, change("e-cancel", "t-1", service.StatusCancelled, start, start)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Apply(ctx, change("e-book", "t-1", service.StatusScheduled, start, start.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Timeline(ctx, "pat-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != service.StatusCancelled || got[0].CancelReason != "patient_request" {
		t.Fatalf("want one cancelled encounter, got %+v", got)
	}
	if err := repo.AddNote(ctx, "t-1", service.Note{ID: "n-1", Text: "x", AuthorRole: "doctor", CreatedAt: start}); !errors.Is(err, service.ErrCancelled) {
		t.Fatalf("want ErrCancelled on a cancelled encounter, got %v", err)
	}
}

func TestRedeliveredEventChangesNothing(t *testing.T) {
	repo := startRepo(t)
	ctx := context.Background()
	if dup, err := repo.Apply(ctx, change("e-1", "t-1", service.StatusCancelled, start, start)); err != nil || dup {
		t.Fatalf("first delivery: dup=%v err=%v", dup, err)
	}
	before := rawEncounter(t, repo, "t-1")
	dup, err := repo.Apply(ctx, change("e-1", "t-1", service.StatusCancelled, start, start.Add(time.Hour)))
	if err != nil || !dup {
		t.Fatalf("redelivery: want duplicate, got dup=%v err=%v", dup, err)
	}
	if after := rawEncounter(t, repo, "t-1"); !equalDocs(before, after) {
		t.Fatalf("redelivery changed the document:\nbefore %v\nafter  %v", before, after)
	}
	if n, _ := repo.DB.Collection(repository.ProcessedEvents).CountDocuments(ctx, bson.M{}); n != 1 {
		t.Fatalf("want 1 processed event, got %d", n)
	}
}

func TestTimelineIsSortedNewestFirstWithNotes(t *testing.T) {
	repo := startRepo(t)
	ctx := context.Background()
	for i, id := range []string{"t-old", "t-new", "t-mid"} {
		at := start.Add(map[string]time.Duration{"t-old": 0, "t-mid": 24 * time.Hour, "t-new": 48 * time.Hour}[id])
		if _, err := repo.Apply(ctx, change("e-"+id, id, service.StatusScheduled, at, start)); err != nil {
			t.Fatal(i, err)
		}
	}
	other := change("e-other", "t-other", service.StatusScheduled, start, start)
	other.PatientID = "pat-002"
	if _, err := repo.Apply(ctx, other); err != nil {
		t.Fatal(err)
	}
	note := service.Note{ID: "n-1", Text: "Control en 30 dias", AuthorRole: "doctor", CreatedAt: start}
	if err := repo.AddNote(ctx, "t-mid", note); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddNote(ctx, "t-missing", note); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	got, err := repo.Timeline(ctx, "pat-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].AppointmentID != "t-new" || got[1].AppointmentID != "t-mid" || got[2].AppointmentID != "t-old" {
		t.Fatalf("want newest first and only pat-001, got %+v", got)
	}
	if len(got[1].Notes) != 1 || got[1].Notes[0].Text != note.Text {
		t.Fatalf("note not returned: %+v", got[1].Notes)
	}
}

func rawEncounter(t *testing.T, repo *repository.Repo, id string) bson.D {
	t.Helper()
	var doc bson.D
	if err := repo.DB.Collection(repository.Encounters).FindOne(context.Background(), bson.M{"_id": id}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func equalDocs(a, b bson.D) bool {
	ab, _ := bson.Marshal(a)
	bb, _ := bson.Marshal(b)
	return string(ab) == string(bb)
}
