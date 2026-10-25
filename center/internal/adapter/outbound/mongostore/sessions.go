// Package mongostore хранит в MongoDB то, что должно пережить
// перезапуск центра: занятия экзаменуемых и сессии операторов.
//
// Телеметрию пишет sink.Mongo; здесь — состояние, потеря которого
// означает потерю зачёта или разлогиненную смену.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collSessions — занятия: и открытые, и закрытые. Одна коллекция:
// открытое занятие отличается нулевым временем завершения, и искать
// его в двух местах значило бы рассинхронизировать их при сбое.
const collSessions = "training_sessions"

// Sessions — занятия в MongoDB.
type Sessions struct {
	coll *mongo.Collection
}

type sessionDoc struct {
	ID       string     `bson:"_id"`
	Room     string     `bson:"room"`
	Section  string     `bson:"section,omitempty"`
	Rooms    []string   `bson:"rooms,omitempty"`
	Examinee string     `bson:"examinee"`
	Team     string     `bson:"team,omitempty"`
	Operator string     `bson:"operator,omitempty"`
	Rescuers []string   `bson:"rescuers,omitempty"`
	Started  time.Time  `bson:"started"`
	Finished *time.Time `bson:"finished"` // nil — занятие идёт
	Reason   string     `bson:"reason,omitempty"`
	Outcome  string     `bson:"outcome,omitempty"`
	Report   string     `bson:"report,omitempty"`

	AtStart  countersDoc `bson:"at_start"`
	AtFinish countersDoc `bson:"at_finish"`

	// Назначения на тренажёры: по ним считается время и результат
	// каждого упражнения отдельно.
	Assignments []assignmentDoc `bson:"assignments,omitempty"`

	// Задания по тренажёрам: устройство -> цель.
	Targets map[string]targetDoc `bson:"targets,omitempty"`

	// Ролики занятия.
	Records []recordDoc `bson:"records,omitempty"`

	// Отметки о прохождении заданий маршрута.
	Tasks map[string]taskMarkDoc `bson:"tasks,omitempty"`

	// Температура помещения за занятие и комментарий администратора.
	Temp    *tempDoc `bson:"temp,omitempty"`
	Comment string   `bson:"comment,omitempty"`

	// Задание «Набор чисел»: код тренировки и число попыток.
	Code     string `bson:"code,omitempty"`
	Attempts int    `bson:"attempts,omitempty"`
}

type taskMarkDoc struct {
	Done bool      `bson:"done"`
	At   time.Time `bson:"at"`
	By   string    `bson:"by,omitempty"`
	Auto bool      `bson:"auto,omitempty"`
}

type tempDoc struct {
	MinC    float64 `bson:"min_c"`
	MaxC    float64 `bson:"max_c"`
	AvgC    float64 `bson:"avg_c"`
	Samples int     `bson:"samples"`
}

type targetDoc struct {
	// Секунды, а не Duration: в базе время хранится числом, иначе его
	// не прочитать ничем, кроме нашего кода.
	Seconds int    `bson:"seconds,omitempty"`
	Reps    uint16 `bson:"reps,omitempty"`
}

type recordDoc struct {
	Camera   string    `bson:"camera"`
	File     string    `bson:"file"`
	Started  time.Time `bson:"started"`
	Finished time.Time `bson:"finished"`
	Bytes    int64     `bson:"bytes,omitempty"`
	Error    string    `bson:"error,omitempty"`
}

type assignmentDoc struct {
	Device  string      `bson:"device"`
	IIN     string      `bson:"iin"`
	Since   time.Time   `bson:"since"`
	Until   *time.Time  `bson:"until,omitempty"`
	AtStart countersDoc `bson:"at_start"`
	AtEnd   countersDoc `bson:"at_end"`
}

type countersDoc struct {
	DistanceM float64 `bson:"distance_m"`
	Floors    uint16  `bson:"floors"`
	Steps     uint16  `bson:"steps"`
	Sets      uint16  `bson:"sets"`
	Reps      uint16  `bson:"reps"`
	MaxLoadKG float64 `bson:"max_load_kg"`
}

// NewSessions готовит коллекцию и индексы.
func NewSessions(ctx context.Context, db *mongo.Database) (*Sessions, error) {
	s := &Sessions{coll: db.Collection(collSessions)}

	// Частичный уникальный индекс: открытое занятие в комнате может
	// быть только одно. Это правило прикладного слоя, но здесь оно
	// защищает от гонки двух операторов, нажавших «начать» разом.
	active := mongo.IndexModel{
		Keys: bson.D{{Key: "room", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.D{{Key: "finished", Value: nil}}).
			SetName("active_room"),
	}
	// Журнал читается по комнате, новые первыми.
	recent := mongo.IndexModel{
		Keys:    bson.D{{Key: "room", Value: 1}, {Key: "finished", Value: -1}},
		Options: options.Index().SetName("room_finished"),
	}
	// История тренировок в карточке спасателя: выборка по ИИН участника.
	byRescuer := mongo.IndexModel{
		Keys:    bson.D{{Key: "rescuers", Value: 1}, {Key: "finished", Value: -1}},
		Options: options.Index().SetName("rescuer_finished"),
	}

	if _, err := s.coll.Indexes().CreateMany(ctx,
		[]mongo.IndexModel{active, recent, byRescuer}); err != nil {
		return nil, fmt.Errorf("индексы занятий: %w", err)
	}
	return s, nil
}

func (s *Sessions) Save(ctx context.Context, sess training.Session) error {
	doc := toDoc(sess)

	_, err := s.coll.ReplaceOne(ctx,
		bson.D{{Key: "_id", Value: doc.ID}}, doc, options.Replace().SetUpsert(true))
	if err != nil {
		// Нарушение уникального индекса означает, что занятие в этой
		// комнате уже открыто: правило то же, что в прикладном слое.
		if mongo.IsDuplicateKeyError(err) {
			return training.ErrBusy
		}
		return fmt.Errorf("сохранение занятия: %w", err)
	}
	return nil
}

func (s *Sessions) Active(ctx context.Context, room string) (training.Session, bool, error) {
	filter := bson.D{{Key: "room", Value: room}, {Key: "finished", Value: nil}}

	var doc sessionDoc
	err := s.coll.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return training.Session{}, false, nil
	}
	if err != nil {
		return training.Session{}, false, fmt.Errorf("чтение занятия: %w", err)
	}
	return fromDoc(doc), true, nil
}

// Get возвращает занятие по идентификатору.
func (s *Sessions) Get(ctx context.Context, id string) (training.Session, bool, error) {
	var doc sessionDoc
	err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return training.Session{}, false, nil
	}
	if err != nil {
		return training.Session{}, false, fmt.Errorf("чтение занятия: %w", err)
	}
	return fromDoc(doc), true, nil
}

func (s *Sessions) Recent(ctx context.Context, room string, limit int) ([]training.Session, error) {
	filter := bson.D{{Key: "finished", Value: bson.D{{Key: "$ne", Value: nil}}}}
	// Пустая комната — весь объект: на экране результатов журнал
	// смотрят целиком.
	if room != "" {
		filter = append(filter, bson.E{Key: "room", Value: room})
	}
	opts := options.Find().SetSort(bson.D{{Key: "finished", Value: -1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}

	cur, err := s.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("чтение журнала занятий: %w", err)
	}
	defer cur.Close(ctx)

	var out []training.Session
	for cur.Next(ctx) {
		var doc sessionDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("разбор занятия: %w", err)
		}
		out = append(out, fromDoc(doc))
	}
	return out, cur.Err()
}

// ByRescuer возвращает закрытые занятия карточки, новые первыми.
func (s *Sessions) ByRescuer(ctx context.Context, iin string, limit int) ([]training.Session, error) {
	filter := bson.D{
		{Key: "rescuers", Value: iin},
		{Key: "finished", Value: bson.D{{Key: "$ne", Value: nil}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "finished", Value: -1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}

	cur, err := s.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("чтение истории карточки: %w", err)
	}
	defer cur.Close(ctx)

	var out []training.Session
	for cur.Next(ctx) {
		var doc sessionDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("разбор занятия: %w", err)
		}
		out = append(out, fromDoc(doc))
	}
	return out, cur.Err()
}

func toDoc(s training.Session) sessionDoc {
	doc := sessionDoc{
		ID:       string(s.ID),
		Room:     s.Room,
		Section:  s.Section,
		Rooms:    s.Rooms,
		Examinee: string(s.Examinee),
		Team:     s.Team,
		Operator: s.Operator,
		Rescuers: s.Rescuers,
		Started:  s.Started,
		Reason:   s.Reason,
		Outcome:  string(s.Outcome),
		Report:   s.Report,
		AtStart:  toCounters(s.AtStart),
		AtFinish: toCounters(s.AtFinish),
	}

	for _, a := range s.Assignments {
		item := assignmentDoc{
			Device:  a.Device,
			IIN:     a.IIN,
			Since:   a.Since,
			AtStart: toCounters(a.AtStart),
			AtEnd:   toCounters(a.AtEnd),
		}
		if !a.Until.IsZero() {
			until := a.Until
			item.Until = &until
		}
		doc.Assignments = append(doc.Assignments, item)
	}

	if len(s.Targets) > 0 {
		doc.Targets = make(map[string]targetDoc, len(s.Targets))
		for device, t := range s.Targets {
			doc.Targets[device] = targetDoc{
				Seconds: int(t.Duration.Seconds()),
				Reps:    t.Reps,
			}
		}
	}

	if len(s.Tasks) > 0 {
		doc.Tasks = make(map[string]taskMarkDoc, len(s.Tasks))
		for id, m := range s.Tasks {
			doc.Tasks[id] = taskMarkDoc{Done: m.Done, At: m.At, By: m.By, Auto: m.Auto}
		}
	}

	if s.Temp.Known() {
		doc.Temp = &tempDoc{
			MinC: s.Temp.MinC, MaxC: s.Temp.MaxC,
			AvgC: s.Temp.AvgC, Samples: s.Temp.Samples,
		}
	}
	doc.Comment = s.Comment
	doc.Code = s.Code
	doc.Attempts = s.Attempts

	for _, r := range s.Records {
		doc.Records = append(doc.Records, recordDoc{
			Camera: r.Camera, File: r.File, Started: r.Started,
			Finished: r.Finished, Bytes: r.Bytes, Error: r.Error,
		})
	}
	// Для открытого занятия поле остаётся null — на нём держится
	// частичный индекс «одно занятие на комнату».
	if !s.Finished.IsZero() {
		finished := s.Finished
		doc.Finished = &finished
	}
	return doc
}

func fromDoc(d sessionDoc) training.Session {
	s := training.Session{
		ID:       training.SessionID(d.ID),
		Room:     d.Room,
		Section:  d.Section,
		Rooms:    d.Rooms,
		Examinee: training.Examinee(d.Examinee),
		Team:     d.Team,
		Operator: d.Operator,
		Rescuers: d.Rescuers,
		Started:  d.Started,
		Reason:   d.Reason,
		Outcome:  training.Outcome(d.Outcome),
		Report:   d.Report,
		AtStart:  fromCounters(d.AtStart),
		AtFinish: fromCounters(d.AtFinish),
	}
	if d.Finished != nil {
		s.Finished = *d.Finished
	}

	for _, a := range d.Assignments {
		item := training.Assignment{
			Device:  a.Device,
			IIN:     a.IIN,
			Since:   a.Since,
			AtStart: fromCounters(a.AtStart),
			AtEnd:   fromCounters(a.AtEnd),
		}
		if a.Until != nil {
			item.Until = *a.Until
		}
		s.Assignments = append(s.Assignments, item)
	}

	if len(d.Targets) > 0 {
		s.Targets = make(map[string]training.Target, len(d.Targets))
		for device, t := range d.Targets {
			s.Targets[device] = training.Target{
				Duration: time.Duration(t.Seconds) * time.Second,
				Reps:     t.Reps,
			}
		}
	}

	if len(d.Tasks) > 0 {
		s.Tasks = make(map[string]training.TaskMark, len(d.Tasks))
		for id, m := range d.Tasks {
			s.Tasks[id] = training.TaskMark{Done: m.Done, At: m.At, By: m.By, Auto: m.Auto}
		}
	}

	if d.Temp != nil {
		s.Temp = training.Temp{
			MinC: d.Temp.MinC, MaxC: d.Temp.MaxC,
			AvgC: d.Temp.AvgC, Samples: d.Temp.Samples,
		}
	}
	s.Comment = d.Comment
	s.Code = d.Code
	s.Attempts = d.Attempts

	for _, r := range d.Records {
		s.Records = append(s.Records, training.Record{
			Camera: r.Camera, File: r.File, Started: r.Started,
			Finished: r.Finished, Bytes: r.Bytes, Error: r.Error,
		})
	}
	return s
}

func toCounters(c training.Counters) countersDoc {
	return countersDoc{
		DistanceM: c.DistanceM, Floors: c.Floors, Steps: c.Steps,
		Sets: c.Sets, Reps: c.Reps, MaxLoadKG: c.MaxLoadKG,
	}
}

func fromCounters(d countersDoc) training.Counters {
	return training.Counters{
		DistanceM: d.DistanceM, Floors: d.Floors, Steps: d.Steps,
		Sets: d.Sets, Reps: d.Reps, MaxLoadKG: d.MaxLoadKG,
	}
}
