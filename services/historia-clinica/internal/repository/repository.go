// Package repository is the MongoDB data layer of Historia clinica (ADR-003):
// one document per encounter, referenced by patientId, plus an inbox of
// processed event ids that makes the consumer idempotent.
package repository

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"portal-paciente-clinica/services/historia-clinica/internal/service"
)

const (
	Encounters      = "encounters"
	ProcessedEvents = "processed_events"
	schemaVersion   = 1
)

type Repo struct {
	Client *mongo.Client
	DB     *mongo.Database
}

var _ service.Store = (*Repo)(nil)

// Open connects, checks connectivity and creates validators and indexes.
func Open(ctx context.Context, uri, database string) (*Repo, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(3 * time.Second))
	if err != nil {
		return nil, err
	}
	r := &Repo{Client: client, DB: client.Database(database)}
	if err := r.Ping(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	if err := r.migrate(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return r, nil
}

// encounterSchema validates the mandatory fields (ADR-003); notes stay flexible
// beyond their required text, which is the point of the document model.
var encounterSchema = bson.M{"$jsonSchema": bson.M{
	"bsonType": "object",
	"required": bson.A{"_id", "patientId", "doctorId", "startAt", "status", "notes", "schemaVersion", "updatedAt"},
	"properties": bson.M{
		"patientId":     bson.M{"bsonType": "string", "minLength": 1},
		"doctorId":      bson.M{"bsonType": "string", "minLength": 1},
		"startAt":       bson.M{"bsonType": "date"},
		"status":        bson.M{"enum": bson.A{service.StatusScheduled, service.StatusCancelled}},
		"schemaVersion": bson.M{"bsonType": "int"},
		"notes": bson.M{"bsonType": "array", "items": bson.M{
			"bsonType": "object", "required": bson.A{"id", "text", "createdAt"},
			"properties": bson.M{"text": bson.M{"bsonType": "string", "minLength": 1, "maxLength": service.MaxNoteLength}},
		}},
	},
}}

func (r *Repo) migrate(ctx context.Context) error {
	err := r.DB.CreateCollection(ctx, Encounters, options.CreateCollection().SetValidator(encounterSchema))
	var cmdErr mongo.CommandError
	if err != nil && !(errors.As(err, &cmdErr) && cmdErr.Code == 48) { // 48: NamespaceExists
		return err
	}
	_, err = r.DB.Collection(Encounters).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "patientId", Value: 1}, {Key: "startAt", Value: -1}}, Options: options.Index().SetName("patient_timeline"),
	})
	// processed_events uses the event id as _id: the primary key is the unique constraint.
	return err
}

func (r *Repo) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return r.Client.Ping(ctx, nil)
}

// Apply has no multi-document transaction (standalone MongoDB): the encounter
// update is idempotent by itself and the inbox marker is written after it, so
// a crash in between only replays an operation that converges to the same state.
func (r *Repo) Apply(ctx context.Context, c service.Change) (bool, error) {
	inbox := r.DB.Collection(ProcessedEvents)
	err := inbox.FindOne(ctx, bson.M{"_id": c.EventID}).Err()
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return false, err
	}
	insertOnly := bson.M{"patientId": c.PatientID, "doctorId": c.DoctorID, "startAt": c.StartAt,
		"notes": bson.A{}, "schemaVersion": schemaVersion, "createdAt": c.At}
	var update bson.M
	if c.Status == service.StatusCancelled {
		update = bson.M{"$set": bson.M{"status": service.StatusCancelled, "cancelReason": c.CancelReason, "updatedAt": c.At},
			"$setOnInsert": insertOnly}
	} else {
		// A booking only creates the encounter: if a cancellation arrived first
		// (out of order), the existing cancelled document stays untouched.
		insertOnly["status"] = service.StatusScheduled
		insertOnly["updatedAt"] = c.At
		update = bson.M{"$setOnInsert": insertOnly}
	}
	if _, err := r.DB.Collection(Encounters).UpdateOne(ctx, bson.M{"_id": c.AppointmentID}, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return false, err
	}
	_, err = inbox.InsertOne(ctx, bson.M{"_id": c.EventID, "appointmentId": c.AppointmentID, "status": c.Status, "processedAt": c.At})
	if mongo.IsDuplicateKeyError(err) {
		return true, nil
	}
	return false, err
}

func (r *Repo) AddNote(ctx context.Context, appointmentID string, n service.Note) error {
	encounters := r.DB.Collection(Encounters)
	res, err := encounters.UpdateOne(ctx, bson.M{"_id": appointmentID, "status": service.StatusScheduled},
		bson.M{"$push": bson.M{"notes": n}, "$set": bson.M{"updatedAt": n.CreatedAt}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 1 {
		return nil
	}
	err = encounters.FindOne(ctx, bson.M{"_id": appointmentID}).Err()
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return service.ErrNotFound
	case err != nil:
		return err
	}
	return service.ErrCancelled
}

func (r *Repo) Timeline(ctx context.Context, patientID string) ([]service.Encounter, error) {
	cur, err := r.DB.Collection(Encounters).Find(ctx, bson.M{"patientId": patientID},
		options.Find().SetSort(bson.D{{Key: "startAt", Value: -1}}))
	if err != nil {
		return nil, err
	}
	out := []service.Encounter{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) Close(ctx context.Context) error { return r.Client.Disconnect(ctx) }
