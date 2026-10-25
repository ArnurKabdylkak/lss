package rooms

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Авария закрывает занятие, а закрытие само переводит комнату. Если
// авария при этом держит блокировку комнат, центр зависает и зал не
// обесточивается — так и было на объекте.
func TestАварияСОткрытымЗанятиемНеЗависает(t *testing.T) {
	ctx := context.Background()
	obj := layout.Object{Site: "obj-17", Rooms: []layout.Room{{
		ID: "room-1", Name: "Теплокамера",
		Equipment: []layout.UnitSpec{{Unit: "light", Alarm: "on"}},
	}}}

	sessions := repo.NewSessions()
	if err := sessions.Save(ctx, training.Session{ID: "s-1", Room: "room-1"}); err != nil {
		t.Fatal(err)
	}

	var svc *Service
	finish := func(ctx context.Context, room layout.RoomID, reason string, alarm bool) error {
		// Как настоящее закрытие: оно трогает состояние комнаты.
		_, err := svc.SetRoster(ctx, room, "", nil, "закрытие")
		return err
	}
	svc = New(stubLayout{obj: obj}, repo.NewMemory(), repo.NewStates(), sessions,
		stubStreams{}, nil, nil, nil, nil, nil, repo.NewAlarms(), finish, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	done := make(chan error, 1)
	go func() {
		_, err := svc.Alarm(ctx, "проверка", "оператор")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("авария зависла: закрытие занятия ждёт блокировку, которую держит авария")
	}

	st, _ := svc.State(ctx, "room-1")
	if st.State != layout.StateAlarm {
		t.Errorf("комната не в аварии: %s", st.State)
	}
}
