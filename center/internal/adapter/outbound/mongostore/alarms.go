package mongostore

import (
	"context"
	"fmt"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collAlarms — журнал аварийных остановок. Без срока хранения:
// авария — то, ради чего потом поднимают записи.
const collAlarms = "alarms"

// Alarms — журнал аварий в MongoDB.
type Alarms struct {
	coll *mongo.Collection
}

type alarmDoc struct {
	ID       string     `bson:"_id"`
	At       time.Time  `bson:"at"`
	Reason   string     `bson:"reason,omitempty"`
	Author   string     `bson:"author,omitempty"`
	Rooms    []string   `bson:"rooms,omitempty"`
	Sessions []string   `bson:"sessions,omitempty"`
	Reset    *time.Time `bson:"reset,omitempty"`
}

func NewAlarms(ctx context.Context, db *mongo.Database) (*Alarms, error) {
	a := &Alarms{coll: db.Collection(collAlarms)}

	idx := mongo.IndexModel{
		Keys:    bson.D{{Key: "at", Value: -1}},
		Options: options.Index().SetName("at"),
	}
	if _, err := a.coll.Indexes().CreateOne(ctx, idx); err != nil {
		return nil, fmt.Errorf("индекс журнала аварий: %w", err)
	}
	return a, nil
}

func (a *Alarms) Add(ctx context.Context, e layout.AlarmEvent) error {
	doc := alarmDoc{
		ID: e.ID, At: e.At, Reason: e.Reason, Author: e.Author,
		Sessions: e.Sessions,
	}
	for _, r := range e.Rooms {
		doc.Rooms = append(doc.Rooms, string(r))
	}

	if _, err := a.coll.InsertOne(ctx, doc); err != nil {
		return fmt.Errorf("запись в журнал аварий: %w", err)
	}
	return nil
}

func (a *Alarms) Recent(ctx context.Context, limit int) ([]layout.AlarmEvent, error) {
	opts := options.Find().SetSort(bson.D{{Key: "at", Value: -1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}

	cur, err := a.coll.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, fmt.Errorf("чтение журнала аварий: %w", err)
	}
	defer cur.Close(ctx)

	out := make([]layout.AlarmEvent, 0)
	for cur.Next(ctx) {
		var doc alarmDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("разбор записи журнала: %w", err)
		}
		e := layout.AlarmEvent{
			ID: doc.ID, At: doc.At, Reason: doc.Reason,
			Author: doc.Author, Sessions: doc.Sessions,
		}
		for _, r := range doc.Rooms {
			e.Rooms = append(e.Rooms, layout.RoomID(r))
		}
		if doc.Reset != nil {
			e.Reset = *doc.Reset
		}
		out = append(out, e)
	}
	return out, cur.Err()
}

func (a *Alarms) Resolve(ctx context.Context, id string, at time.Time) error {
	_, err := a.coll.UpdateByID(ctx, id,
		bson.D{{Key: "$set", Value: bson.D{{Key: "reset", Value: at}}}})
	if err != nil {
		return fmt.Errorf("снятие аварии в журнале: %w", err)
	}
	return nil
}
