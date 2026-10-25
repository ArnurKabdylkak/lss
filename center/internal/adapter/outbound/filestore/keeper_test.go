package filestore

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Смена, записанная до перезапуска, должна найтись после него.
//
// Ради этого хранилище и заводилось: без него закрытое занятие
// исчезало вместе с процессом, а вместе с ним — зачёт звена.
func TestДанныеПереживаютПерезапуск(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "state.json")

	// Первый запуск: заводим карточку, состояние комнаты и аварию.
	cards := repo.NewCards()
	states := repo.NewStates()
	alarms := repo.NewAlarms()
	sessions := repo.NewSessions()

	if err := cards.Create(ctx, rescuer.Card{IIN: "900315501234", Name: "Иванов Пётр"}); err != nil {
		t.Fatalf("карточка не заведена: %v", err)
	}
	if err := states.Save(ctx, layout.RoomState{
		Room: "room-1", State: layout.StateAlarm, Reason: "проверка",
	}); err != nil {
		t.Fatalf("состояние не сохранено: %v", err)
	}
	if err := alarms.Add(ctx, layout.AlarmEvent{
		ID: "a1", At: time.Now(), Reason: "проверка", Author: "оператор",
	}); err != nil {
		t.Fatalf("авария не записана: %v", err)
	}
	if err := sessions.Save(ctx, training.Session{
		ID: "s1", Room: "room-1", Team: "звено 1",
	}); err != nil {
		t.Fatalf("занятие не сохранено: %v", err)
	}

	keeper := New(path, sessions, cards, states, alarms, quiet())
	if err := keeper.Save(); err != nil {
		t.Fatalf("снимок не записан: %v", err)
	}

	// Второй запуск: новые пустые хранилища читают тот же файл.
	cards2 := repo.NewCards()
	states2 := repo.NewStates()
	alarms2 := repo.NewAlarms()
	sessions2 := repo.NewSessions()

	if err := New(path, sessions2, cards2, states2, alarms2, quiet()).Load(); err != nil {
		t.Fatalf("снимок не прочитан: %v", err)
	}

	card, ok, err := cards2.Get(ctx, "900315501234")
	if err != nil || !ok || card.Name != "Иванов Пётр" {
		t.Errorf("карточка не восстановлена: %+v, %v, %v", card, ok, err)
	}

	st, ok, err := states2.Get(ctx, "room-1")
	if err != nil || !ok || st.State != layout.StateAlarm {
		t.Errorf("состояние комнаты не восстановлено: %+v, %v, %v", st, ok, err)
	}

	events, err := alarms2.Recent(ctx, 10)
	if err != nil || len(events) != 1 || events[0].Reason != "проверка" {
		t.Errorf("журнал аварий не восстановлен: %+v, %v", events, err)
	}

	sess, ok, err := sessions2.Active(ctx, "room-1")
	if err != nil || !ok || sess.Team != "звено 1" {
		t.Errorf("занятие не восстановлено: %+v, %v, %v", sess, ok, err)
	}
}

// Первый запуск — это отсутствующий файл, а не ошибка.
func TestПервыйЗапускБезФайла(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	k := New(path, repo.NewSessions(), repo.NewCards(), repo.NewStates(),
		repo.NewAlarms(), quiet())

	if err := k.Load(); err != nil {
		t.Errorf("отсутствие файла принято за ошибку: %v", err)
	}
}
