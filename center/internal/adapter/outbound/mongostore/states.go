package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collStates — состояния комнат. Документ на комнату, ключ — её
// идентификатор: история переходов пишется в журнал, здесь только
// текущее положение дел.
const collStates = "room_states"

// States — состояния комнат в MongoDB.
type States struct {
	coll *mongo.Collection
}

type stateDoc struct {
	Room   string    `bson:"_id"`
	State  string    `bson:"state"`
	Since  time.Time `bson:"since"`
	Reason string    `bson:"reason,omitempty"`
	Author string    `bson:"author,omitempty"`
	Code   string    `bson:"code,omitempty"`

	// Подготовка хранится рядом с состоянием: это одно положение дел,
	// и в двух записях они разошлись бы.
	Prep *prepDoc `bson:"prep,omitempty"`

	// Equipment — какие узлы оборудования включены (подтверждённо).
	Equipment map[string]bool `bson:"equipment,omitempty"`

	// Pending — узлы, ждущие подтверждения сборщика.
	Pending map[string]bool `bson:"pending,omitempty"`

	// PendingSince — когда по узлу ушла команда. Хранится вместе с
	// ожиданием: без отметки центр после перезапуска не знал бы, с
	// какого момента отсчитывать срок, и ожидание висело бы вечно.
	PendingSince map[string]time.Time `bson:"pending_since,omitempty"`

	// UnitSince и Manual — для выдержек и автоматики узлов.
	UnitSince map[string]time.Time `bson:"unit_since,omitempty"`
	Manual    map[string]bool      `bson:"manual,omitempty"`

	// Roster — состав, записанный до подготовки: он переживает
	// перезапуск центра так же, как прогрев и код.
	Roster *rosterDoc `bson:"roster,omitempty"`
}

type rosterDoc struct {
	Team     string    `bson:"team,omitempty"`
	Rescuers []string  `bson:"rescuers"`
	Author   string    `bson:"author,omitempty"`
	Since    time.Time `bson:"since"`
}

type prepDoc struct {
	TargetC float64   `bson:"target_c,omitempty"`
	Started time.Time `bson:"started"`
	Until   time.Time `bson:"until,omitempty"`
	Reached time.Time `bson:"reached,omitempty"`
}

func NewStates(_ context.Context, db *mongo.Database) (*States, error) {
	return &States{coll: db.Collection(collStates)}, nil
}

func (s *States) Save(ctx context.Context, st layout.RoomState) error {
	doc := stateDoc{
		Room:   string(st.Room),
		State:  string(st.State),
		Since:  st.Since,
		Reason: st.Reason,
		Author: st.Author,
		Code:   st.Code,
	}
	if len(st.Equipment) > 0 {
		doc.Equipment = st.Equipment
	}
	if len(st.Pending) > 0 {
		doc.Pending = st.Pending
	}
	if len(st.PendingSince) > 0 {
		doc.PendingSince = st.PendingSince
	}
	if len(st.UnitSince) > 0 {
		doc.UnitSince = st.UnitSince
	}
	if len(st.Manual) > 0 {
		doc.Manual = st.Manual
	}
	if st.Roster.Set() {
		doc.Roster = &rosterDoc{
			Team:     st.Roster.Team,
			Rescuers: st.Roster.Rescuers,
			Author:   st.Roster.Author,
			Since:    st.Roster.Since,
		}
	}
	if st.Prep.Running() {
		doc.Prep = &prepDoc{
			TargetC: st.Prep.TargetC,
			Started: st.Prep.Started,
			Until:   st.Prep.Until,
			Reached: st.Prep.Reached,
		}
	}
	_, err := s.coll.ReplaceOne(ctx,
		bson.D{{Key: "_id", Value: doc.Room}}, doc, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("сохранение состояния комнаты: %w", err)
	}
	return nil
}

func (s *States) Get(ctx context.Context, room layout.RoomID) (layout.RoomState, bool, error) {
	var doc stateDoc
	err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: string(room)}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return layout.RoomState{}, false, nil
	}
	if err != nil {
		return layout.RoomState{}, false, fmt.Errorf("чтение состояния комнаты: %w", err)
	}
	return fromStateDoc(doc), true, nil
}

func (s *States) All(ctx context.Context) ([]layout.RoomState, error) {
	cur, err := s.coll.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("чтение состояний комнат: %w", err)
	}
	defer cur.Close(ctx)

	out := make([]layout.RoomState, 0)
	for cur.Next(ctx) {
		var doc stateDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("разбор состояния комнаты: %w", err)
		}
		out = append(out, fromStateDoc(doc))
	}
	return out, cur.Err()
}

func fromStateDoc(d stateDoc) layout.RoomState {
	st := layout.RoomState{
		Room:   layout.RoomID(d.Room),
		State:  layout.State(d.State),
		Since:  d.Since,
		Reason: d.Reason,
		Author: d.Author,
		Code:   d.Code,
	}
	st.Equipment = d.Equipment
	st.Pending = d.Pending
	st.PendingSince = d.PendingSince
	st.UnitSince = d.UnitSince
	st.Manual = d.Manual
	if d.Roster != nil {
		st.Roster = layout.Roster{
			Team:     d.Roster.Team,
			Rescuers: d.Roster.Rescuers,
			Author:   d.Roster.Author,
			Since:    d.Roster.Since,
		}
	}
	if d.Prep != nil {
		st.Prep = layout.Preparation{
			TargetC: d.Prep.TargetC,
			Started: d.Prep.Started,
			Until:   d.Prep.Until,
			Reached: d.Prep.Reached,
		}
	}
	return st
}
