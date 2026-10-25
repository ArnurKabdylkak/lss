package mongostore

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Тесты идут против настоящей MongoDB: подменять драйвер заглушкой
// бессмысленно — проверяются как раз индексы и поведение базы.
//
//	MCHS_TEST_MONGO=mongodb://127.0.0.1:27018 go test ./...
func database(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("MCHS_TEST_MONGO")
	if uri == "" {
		t.Skip("MCHS_TEST_MONGO не задан: проверка хранилища пропущена")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("подключение к MongoDB: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("MongoDB не отвечает: %v", err)
	}

	// Своя база на каждый прогон: тесты не должны мешать друг другу.
	// Точка в имени базы недопустима, поэтому время без неё.
	db := client.Database("mchs_test_" + time.Now().Format("150405") +
		"_" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.Drop(cleanup)
		_ = client.Disconnect(cleanup)
	})
	return db
}

func TestSessionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewSessions(ctx, database(t))
	if err != nil {
		t.Fatalf("NewSessions() вернул ошибку: %v", err)
	}

	open := training.Session{
		ID: "s1", Room: "room-1", Examinee: "Иванов Пётр", Operator: "Дежурный",
		Started: time.Now().Truncate(time.Millisecond),
		AtStart: training.Counters{DistanceM: 800, Floors: 12},
	}
	if err := store.Save(ctx, open); err != nil {
		t.Fatalf("Save(открытое) вернул ошибку: %v", err)
	}

	got, ok, err := store.Active(ctx, "room-1")
	if err != nil || !ok {
		t.Fatalf("Active() = ok %v, err %v", ok, err)
	}
	if got.Examinee != "Иванов Пётр" || !got.Active() {
		t.Errorf("прочитано не то: %+v", got)
	}

	// Второе открытое занятие в комнате не пускает уникальный индекс:
	// иначе показания зачлись бы двоим.
	dup := training.Session{ID: "s2", Room: "room-1", Examinee: "Петров Иван", Started: time.Now()}
	if err := store.Save(ctx, dup); !errors.Is(err, training.ErrBusy) {
		t.Errorf("второе занятие = %v, ожидалось ErrBusy", err)
	}

	// Закрываем: занятие уходит из открытых в журнал.
	open.Finished = time.Now().Truncate(time.Millisecond)
	open.AtFinish = training.Counters{DistanceM: 2300, Floors: 20}
	if err := store.Save(ctx, open); err != nil {
		t.Fatalf("Save(закрытое) вернул ошибку: %v", err)
	}

	if _, ok, _ := store.Active(ctx, "room-1"); ok {
		t.Error("после закрытия комната должна быть свободна")
	}

	recent, err := store.Recent(ctx, "room-1", 10)
	if err != nil || len(recent) != 1 {
		t.Fatalf("Recent() = %d шт., err %v", len(recent), err)
	}
	if r := recent[0].Result(); r.DistanceM != 1500 || r.Floors != 8 {
		t.Errorf("результат из базы = %+v", r)
	}

	// Комната освободилась — новое занятие открывается.
	if err := store.Save(ctx, dup); err != nil {
		t.Errorf("занятие после закрытия прошлого = %v", err)
	}
}

func TestAuthSessionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewAuthSessions(ctx, database(t))
	if err != nil {
		t.Fatalf("NewAuthSessions() вернул ошибку: %v", err)
	}

	live := auth.Session{
		Token: "t-live", Login: "operator", Name: "Дежурный",
		Role: auth.RoleOperator, Expires: time.Now().Add(time.Hour),
	}
	expired := auth.Session{
		Token: "t-old", Login: "viewer", Role: auth.RoleViewer,
		Expires: time.Now().Add(-time.Hour),
	}
	for _, s := range []auth.Session{live, expired} {
		if err := store.Save(ctx, s); err != nil {
			t.Fatalf("Save(%s) вернул ошибку: %v", s.Token, err)
		}
	}

	// Просроченную не отдаём, даже если уборка базы до неё не дошла.
	all, err := store.All(ctx)
	if err != nil {
		t.Fatalf("All() вернул ошибку: %v", err)
	}
	if len(all) != 1 || all[0].Token != "t-live" || all[0].Role != auth.RoleOperator {
		t.Fatalf("All() = %+v", all)
	}

	if err := store.Delete(ctx, "t-live"); err != nil {
		t.Fatalf("Delete() вернул ошибку: %v", err)
	}
	if all, _ := store.All(ctx); len(all) != 0 {
		t.Errorf("после выхода сессия осталась: %+v", all)
	}
}

func TestCardsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := database(t)

	store, err := NewCards(ctx, db)
	if err != nil {
		t.Fatalf("NewCards() вернул ошибку: %v", err)
	}

	card := rescuer.Card{
		IIN: "900315501234", Name: "Иванов Пётр Сергеевич",
		Unit: "СЧ-2", Created: time.Now().Truncate(time.Millisecond), Author: "Дежурный",
	}
	if err := store.Create(ctx, card); err != nil {
		t.Fatalf("Create() вернул ошибку: %v", err)
	}

	// Повтор ИИН не допускается: вторая карточка разорвала бы историю
	// человека надвое.
	dup := rescuer.Card{IIN: card.IIN, Name: "Иванов П. С.", Created: time.Now()}
	if err := store.Create(ctx, dup); !errors.Is(err, rescuer.ErrDuplicate) {
		t.Errorf("повтор ИИН = %v, ожидалось ErrDuplicate", err)
	}

	got, ok, err := store.Get(ctx, card.IIN)
	if err != nil || !ok {
		t.Fatalf("Get() = ok %v, err %v", ok, err)
	}
	if got.Name != card.Name || got.Unit != "СЧ-2" || got.Author != "Дежурный" {
		t.Errorf("прочитано не то: %+v", got)
	}

	// Поиск без учёта регистра по ФИО, ИИН и подразделению.
	for _, q := range []string{"иванов", "9003155", "сч-2"} {
		list, err := store.List(ctx, q, false)
		if err != nil || len(list) != 1 {
			t.Errorf("List(%q) = %d шт., err %v", q, len(list), err)
		}
	}

	// Правка меняет ФИО, но не трогает, кто и когда завёл карточку.
	got.Name = "Иванов Пётр"
	got.Archived = true
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("Update() вернул ошибку: %v", err)
	}
	after, _, _ := store.Get(ctx, card.IIN)
	if after.Name != "Иванов Пётр" || !after.Archived {
		t.Errorf("правка не применилась: %+v", after)
	}
	if after.Author != "Дежурный" || !after.Created.Equal(card.Created) {
		t.Errorf("правка затёрла сведения о заведении: %+v", after)
	}

	// Архивные в списке не показываются, пока их не попросят.
	if list, _ := store.List(ctx, "", false); len(list) != 0 {
		t.Errorf("архивная карточка попала в список: %+v", list)
	}
	if list, _ := store.List(ctx, "", true); len(list) != 1 {
		t.Error("архивная карточка не найдена при запросе с архивом")
	}

	if err := store.Update(ctx, rescuer.Card{IIN: "нет-такой", Name: "Нет Его"}); !errors.Is(err, rescuer.ErrNotFound) {
		t.Errorf("правка несуществующей карточки = %v, ожидалось ErrNotFound", err)
	}
}

func TestSessionsByRescuer(t *testing.T) {
	ctx := context.Background()
	store, err := NewSessions(ctx, database(t))
	if err != nil {
		t.Fatalf("NewSessions() вернул ошибку: %v", err)
	}

	finished := time.Now().Truncate(time.Millisecond)
	mine := training.Session{
		ID: "s1", Room: "room-1", Examinee: "Иванов Пётр",
		Rescuers: []string{"900315501234", "сл-17"},
		Started:  finished.Add(-time.Hour), Finished: finished,
	}
	other := training.Session{
		ID: "s2", Room: "room-1", Examinee: "Петров Иван",
		Rescuers: []string{"сл-18"},
		Started:  finished.Add(-2 * time.Hour), Finished: finished.Add(-time.Hour),
	}
	for _, s := range []training.Session{mine, other} {
		if err := store.Save(ctx, s); err != nil {
			t.Fatalf("Save(%s) вернул ошибку: %v", s.ID, err)
		}
	}

	// История карточки — её занятия, и только они.
	list, err := store.ByRescuer(ctx, "900315501234", 10)
	if err != nil || len(list) != 1 || list[0].ID != "s1" {
		t.Fatalf("ByRescuer() = %+v, err %v", list, err)
	}

	// Занятие звена попадает в историю каждого участника.
	if list, _ := store.ByRescuer(ctx, "сл-17", 10); len(list) != 1 {
		t.Error("занятие звена не попало в историю второго участника")
	}
	if list, _ := store.ByRescuer(ctx, "нет-такой", 10); len(list) != 0 {
		t.Error("у неизвестной карточки не должно быть истории")
	}
}
