package repo

import (
	"context"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

func TestSessionsSaveReplacesFinished(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewSessions()

	sess := training.Session{
		ID: "s1", Room: "room-1", Examinee: "Иванов Пётр",
		Rescuers: []string{"т-1"},
		Started:  time.Now().Add(-time.Minute), Finished: time.Now(),
	}
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// Повторное сохранение того же занятия заменяет запись: иначе
	// правка причины раздвоила бы тренировку в карточке.
	sess.Reason = "аварийная остановка"
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}

	past, err := store.Recent(ctx, "room-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("занятий в журнале %d, ожидалось одно", len(past))
	}
	if past[0].Reason != "аварийная остановка" {
		t.Errorf("причина не сохранилась: %+v", past[0])
	}

	byCard, err := store.ByRescuer(ctx, "т-1", 10)
	if err != nil || len(byCard) != 1 {
		t.Fatalf("в карточке %d занятий, ожидалось одно", len(byCard))
	}
}
