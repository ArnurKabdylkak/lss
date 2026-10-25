package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/telemetry"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrMongoUnavailable — база не отвечает при запуске. Центр продолжает
// работать без истории: терять поток телеметрии из-за базы нельзя.
var ErrMongoUnavailable = errors.New("MongoDB недоступна")

// Коллекции разделены по сроку жизни, а не по удобству:
//
//	telemetry — показания датчиков, time-series, стареют по TTL;
//	events    — key, relay, камеры: без TTL, ради них потом поднимают архив;
//	devices   — последнее состояние, upsert по устройству.
const (
	collTelemetry = "telemetry"
	collEvents    = "events"
	collDevices   = "devices"
)

type MongoConfig struct {
	URI      string
	Database string

	// Сколько хранить показания датчиков. Ноль — значение по умолчанию.
	// К событиям не применяется никогда.
	Retention time.Duration

	// Через сколько и по скольку сбрасывать накопленное.
	FlushEvery time.Duration
	BatchSize  int

	ConnectTimeout time.Duration
}

func (c *MongoConfig) applyDefaults() {
	if c.Database == "" {
		c.Database = "mchs"
	}
	if c.Retention == 0 {
		c.Retention = 30 * 24 * time.Hour
	}
	if c.FlushEvery == 0 {
		c.FlushEvery = time.Second
	}
	if c.BatchSize == 0 {
		c.BatchSize = 200
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
}

// Mongo пишет поток в MongoDB и отдаёт историю.
//
// Вставлять по документу на пакет расточительно: при 5 пакетах в секунду
// с устройства это 5 круговых обходов базы в секунду на ровном месте.
// Поэтому записи копятся и уходят пачкой.
type Mongo struct {
	cfg    MongoConfig
	client *mongo.Client
	db     *mongo.Database
	log    *slog.Logger

	mu      sync.Mutex
	pending []mongo.WriteModel
	events  []mongo.WriteModel

	flushed chan struct{}
	stop    context.CancelFunc
	done    chan struct{}
}

type document struct {
	Device   string              `bson:"device"`
	Received time.Time           `bson:"received"`
	Source   string              `bson:"source,omitempty"`
	Data     telemetry.Telemetry `bson:"data"`
	Meta     meta                `bson:"meta"`
}

// meta — метаполе time-series коллекции: по нему идёт группировка и сжатие.
type meta struct {
	Device string `bson:"device"`
	Event  string `bson:"event"`
}

func NewMongo(ctx context.Context, cfg MongoConfig, log *slog.Logger) (*Mongo, error) {
	cfg.applyDefaults()

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(cfg.URI))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMongoUnavailable, err)
	}
	if err := client.Ping(connectCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("%w: %v", ErrMongoUnavailable, err)
	}

	m := &Mongo{
		cfg:     cfg,
		client:  client,
		db:      client.Database(cfg.Database),
		log:     log,
		flushed: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}

	if err := m.ensureSchema(connectCtx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}

	bg, stop := context.WithCancel(context.WithoutCancel(ctx))
	m.stop = stop
	go m.loop(bg)

	log.Info("MongoDB подключена", "db", cfg.Database,
		"retention", cfg.Retention, "flush", cfg.FlushEvery)
	return m, nil
}

// Database отдаёт подключение наружу: занятия и сессии операторов
// хранятся в той же базе и не должны поднимать второй пул соединений
// к тому же серверу.
func (m *Mongo) Database() *mongo.Database { return m.db }

// ensureSchema создаёт коллекции и индексы. Повторный вызов безвреден.
func (m *Mongo) ensureSchema(ctx context.Context) error {
	// Показания — time-series: профиль ровно такой, под какой она сделана.
	ts := options.TimeSeries().SetTimeField("received").SetMetaField("meta").SetGranularity("seconds")
	opts := options.CreateCollection().SetTimeSeriesOptions(ts).
		SetExpireAfterSeconds(int64(m.cfg.Retention.Seconds()))

	if err := m.db.CreateCollection(ctx, collTelemetry, opts); err != nil && !isExists(err) {
		return fmt.Errorf("создание коллекции показаний: %w", err)
	}
	// События — обычная коллекция и НИКОГДА не под TTL: срабатывание
	// защиты должно пережить любую политику хранения.
	if err := m.db.CreateCollection(ctx, collEvents); err != nil && !isExists(err) {
		return fmt.Errorf("создание коллекции событий: %w", err)
	}

	idx := mongo.IndexModel{Keys: bson.D{{Key: "device", Value: 1}, {Key: "received", Value: -1}}}
	if _, err := m.db.Collection(collEvents).Indexes().CreateOne(ctx, idx); err != nil {
		return fmt.Errorf("индекс событий: %w", err)
	}
	return nil
}

func isExists(err error) bool {
	var cmdErr mongo.CommandError
	return errors.As(err, &cmdErr) && cmdErr.Name == "NamespaceExists"
}

// Consume копит запись; отправка — пачкой из фоновой петли.
func (m *Mongo) Consume(_ context.Context, p telemetry.Packet) error {
	doc := document{
		Device:   string(p.Device),
		Received: p.Received,
		Data:     p.Data,
		Meta:     meta{Device: string(p.Device), Event: p.Data.EventName()},
	}
	if p.From.IsValid() {
		doc.Source = p.From.String()
	}

	m.mu.Lock()
	m.pending = append(m.pending, mongo.NewInsertOneModel().SetDocument(doc))
	if p.Data.IsEvent() {
		m.events = append(m.events, mongo.NewInsertOneModel().SetDocument(doc))
	}
	full := len(m.pending) >= m.cfg.BatchSize
	m.mu.Unlock()

	m.saveState(doc)

	if full {
		select {
		case m.flushed <- struct{}{}:
		default:
		}
	}
	return nil
}

// saveState держит последнее состояние устройства отдельным документом:
// панель спрашивает его на каждый экран, и вычитывать его из потока
// показаний было бы расточительно.
func (m *Mongo) saveState(doc document) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pending = append(m.pending, mongo.NewUpdateOneModel().
		SetFilter(bson.D{{Key: "_id", Value: doc.Device}}).
		SetUpdate(bson.D{{Key: "$set", Value: bson.D{
			{Key: "received", Value: doc.Received},
			{Key: "source", Value: doc.Source},
			{Key: "data", Value: doc.Data},
		}}}).
		SetUpsert(true))
}

func (m *Mongo) loop(ctx context.Context) {
	defer close(m.done)

	ticker := time.NewTicker(m.cfg.FlushEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.flush(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
			m.flush(ctx)
		case <-m.flushed:
			m.flush(ctx)
		}
	}
}

func (m *Mongo) flush(ctx context.Context) {
	m.mu.Lock()
	pending, events := m.pending, m.events
	m.pending, m.events = nil, nil
	m.mu.Unlock()

	if len(pending) == 0 && len(events) == 0 {
		return
	}

	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Показания и состояние идут вперемешку: разделяем по типу модели.
	var inserts, upserts []mongo.WriteModel
	for _, w := range pending {
		if _, ok := w.(*mongo.UpdateOneModel); ok {
			upserts = append(upserts, w)
			continue
		}
		inserts = append(inserts, w)
	}

	opts := options.BulkWrite().SetOrdered(false)

	if len(inserts) > 0 {
		if _, err := m.db.Collection(collTelemetry).BulkWrite(writeCtx, inserts, opts); err != nil {
			m.log.Error("запись показаний", "err", err, "docs", len(inserts))
		}
	}
	if len(upserts) > 0 {
		if _, err := m.db.Collection(collDevices).BulkWrite(writeCtx, upserts, opts); err != nil {
			m.log.Error("запись состояния", "err", err, "docs", len(upserts))
		}
	}
	if len(events) > 0 {
		if _, err := m.db.Collection(collEvents).BulkWrite(writeCtx, events, opts); err != nil {
			m.log.Error("запись событий", "err", err, "docs", len(events))
		}
	}
}

// History отдаёт прошлое устройства за интервал.
func (m *Mongo) History(ctx context.Context, q port.HistoryQuery) ([]telemetry.Packet, error) {
	coll := collTelemetry
	if q.EventsOnly {
		coll = collEvents
	}

	filter := bson.D{
		{Key: "device", Value: string(q.Device)},
		{Key: "received", Value: bson.D{{Key: "$gte", Value: q.From}, {Key: "$lte", Value: q.To}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "received", Value: 1}})
	if q.Limit > 0 {
		opts.SetLimit(int64(q.Limit))
	}

	cur, err := m.db.Collection(coll).Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("выборка истории: %w", err)
	}
	defer cur.Close(ctx)

	var out []telemetry.Packet
	for cur.Next(ctx) {
		var d document
		if err := cur.Decode(&d); err != nil {
			return nil, fmt.Errorf("разбор документа: %w", err)
		}
		out = append(out, telemetry.Packet{
			Device:   telemetry.DeviceID(d.Device),
			Received: d.Received,
			Data:     d.Data,
		})
	}
	return out, cur.Err()
}

// Restore возвращает последнее известное состояние устройств.
// Вызывается при старте центра, чтобы перезапуск не обнулял панель.
func (m *Mongo) Restore(ctx context.Context) ([]telemetry.Packet, error) {
	cur, err := m.db.Collection(collDevices).Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var out []telemetry.Packet
	for cur.Next(ctx) {
		var d struct {
			ID       string              `bson:"_id"`
			Received time.Time           `bson:"received"`
			Data     telemetry.Telemetry `bson:"data"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, telemetry.Packet{
			Device:   telemetry.DeviceID(d.ID),
			Received: d.Received,
			Data:     d.Data,
		})
	}
	return out, cur.Err()
}

// Close дописывает хвост и закрывает соединение.
func (m *Mongo) Close() error {
	m.stop()
	<-m.done
	return m.client.Disconnect(context.Background())
}
