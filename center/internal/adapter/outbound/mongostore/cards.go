package mongostore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collCards — карточки спасателей. Ключ — ИИН: он и есть
// идентификатор человека в системе.
const collCards = "rescuers"

// Cards — карточки спасателей в MongoDB.
type Cards struct {
	coll *mongo.Collection
}

type cardDoc struct {
	IIN      string    `bson:"_id"`
	Name     string    `bson:"name"`
	Unit     string    `bson:"unit,omitempty"`
	Created  time.Time `bson:"created"`
	Author   string    `bson:"author,omitempty"`
	Archived bool      `bson:"archived"`
}

// NewCards готовит коллекцию и индексы.
func NewCards(ctx context.Context, db *mongo.Database) (*Cards, error) {
	c := &Cards{coll: db.Collection(collCards)}

	// Поиск по ФИО: список карточек открывают, чтобы найти человека.
	byName := mongo.IndexModel{
		Keys:    bson.D{{Key: "name", Value: 1}},
		Options: options.Index().SetName("name"),
	}
	if _, err := c.coll.Indexes().CreateOne(ctx, byName); err != nil {
		return nil, fmt.Errorf("индекс карточек: %w", err)
	}
	return c, nil
}

func (c *Cards) Create(ctx context.Context, card rescuer.Card) error {
	_, err := c.coll.InsertOne(ctx, toCardDoc(card))
	if mongo.IsDuplicateKeyError(err) {
		// Уникальность держит сама база: два администратора могут
		// завести одного человека одновременно, и проверка в коде
		// этого не поймает.
		return rescuer.ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("создание карточки: %w", err)
	}
	return nil
}

func (c *Cards) Update(ctx context.Context, card rescuer.Card) error {
	// Кто и когда завёл карточку, правкой ФИО не меняется.
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "name", Value: string(card.Name)},
		{Key: "unit", Value: card.Unit},
		{Key: "archived", Value: card.Archived},
	}}}

	res, err := c.coll.UpdateByID(ctx, string(card.IIN), update)
	if err != nil {
		return fmt.Errorf("правка карточки: %w", err)
	}
	if res.MatchedCount == 0 {
		return rescuer.ErrNotFound
	}
	return nil
}

func (c *Cards) Get(ctx context.Context, iin rescuer.IIN) (rescuer.Card, bool, error) {
	var doc cardDoc
	err := c.coll.FindOne(ctx, bson.D{{Key: "_id", Value: string(iin)}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return rescuer.Card{}, false, nil
	}
	if err != nil {
		return rescuer.Card{}, false, fmt.Errorf("чтение карточки: %w", err)
	}
	return fromCardDoc(doc), true, nil
}

func (c *Cards) List(ctx context.Context, query string, withArchived bool) ([]rescuer.Card, error) {
	filter := bson.D{}
	if !withArchived {
		filter = append(filter, bson.E{Key: "archived", Value: false})
	}
	if q := regexp.QuoteMeta(query); q != "" {
		// Поиск подстрокой без учёта регистра: администратор ищет
		// «иванов», а в карточке «Иванов».
		rx := bson.D{{Key: "$regex", Value: q}, {Key: "$options", Value: "i"}}
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "name", Value: rx}},
			bson.D{{Key: "_id", Value: rx}},
			bson.D{{Key: "unit", Value: rx}},
		}})
	}

	cur, err := c.coll.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("чтение карточек: %w", err)
	}
	defer cur.Close(ctx)

	out := make([]rescuer.Card, 0)
	for cur.Next(ctx) {
		var doc cardDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("разбор карточки: %w", err)
		}
		out = append(out, fromCardDoc(doc))
	}
	return out, cur.Err()
}

func toCardDoc(c rescuer.Card) cardDoc {
	return cardDoc{
		IIN:      string(c.IIN),
		Name:     string(c.Name),
		Unit:     c.Unit,
		Created:  c.Created,
		Author:   c.Author,
		Archived: c.Archived,
	}
}

func fromCardDoc(d cardDoc) rescuer.Card {
	return rescuer.Card{
		IIN:      rescuer.IIN(d.IIN),
		Name:     rescuer.Name(d.Name),
		Unit:     d.Unit,
		Created:  d.Created,
		Author:   d.Author,
		Archived: d.Archived,
	}
}
