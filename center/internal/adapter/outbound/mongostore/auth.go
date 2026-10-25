package mongostore

import (
	"context"
	"fmt"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collAuth — сессии операторов.
const collAuth = "operator_sessions"

// AuthSessions — сессии операторов в MongoDB.
//
// Токен лежит в базе в открытом виде: это не пароль, он живёт часы и
// отзывается выходом. Хранить его хеш значило бы не уметь продлевать
// сессию по предъявленному токену без полного перебора.
type AuthSessions struct {
	coll *mongo.Collection
}

type authDoc struct {
	Token   string    `bson:"_id"`
	Login   string    `bson:"login"`
	Name    string    `bson:"name,omitempty"`
	Role    string    `bson:"role"`
	Expires time.Time `bson:"expires"`
}

// NewAuthSessions готовит коллекцию и индексы.
func NewAuthSessions(ctx context.Context, db *mongo.Database) (*AuthSessions, error) {
	a := &AuthSessions{coll: db.Collection(collAuth)}

	// Просроченные сессии убирает сама база: иначе коллекция растёт
	// вечно, а центр чистит только то, что помнит в памяти.
	ttl := mongo.IndexModel{
		Keys:    bson.D{{Key: "expires", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0).SetName("expires_ttl"),
	}
	if _, err := a.coll.Indexes().CreateOne(ctx, ttl); err != nil {
		return nil, fmt.Errorf("индекс сессий операторов: %w", err)
	}
	return a, nil
}

func (a *AuthSessions) Save(ctx context.Context, s auth.Session) error {
	doc := authDoc{
		Token:   s.Token,
		Login:   s.Login,
		Name:    s.Name,
		Role:    string(s.Role),
		Expires: s.Expires,
	}
	_, err := a.coll.ReplaceOne(ctx,
		bson.D{{Key: "_id", Value: s.Token}}, doc, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("сохранение сессии: %w", err)
	}
	return nil
}

func (a *AuthSessions) Delete(ctx context.Context, token string) error {
	_, err := a.coll.DeleteOne(ctx, bson.D{{Key: "_id", Value: token}})
	if err != nil {
		return fmt.Errorf("удаление сессии: %w", err)
	}
	return nil
}

func (a *AuthSessions) All(ctx context.Context) ([]auth.Session, error) {
	// Просроченные база удаляет не мгновенно — TTL-уборка ходит раз в
	// минуту, поэтому отсекаем их и запросом.
	filter := bson.D{{Key: "expires", Value: bson.D{{Key: "$gt", Value: time.Now()}}}}

	cur, err := a.coll.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("чтение сессий: %w", err)
	}
	defer cur.Close(ctx)

	var out []auth.Session
	for cur.Next(ctx) {
		var doc authDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("разбор сессии: %w", err)
		}
		out = append(out, auth.Session{
			Token:   doc.Token,
			Login:   doc.Login,
			Name:    doc.Name,
			Role:    auth.Role(doc.Role),
			Expires: doc.Expires,
		})
	}
	return out, cur.Err()
}
