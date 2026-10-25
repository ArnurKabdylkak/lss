package rooms

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// pendingService — центр с одной комнатой и ручным управлением узлами.
// Команды никуда не уходят: проверяется именно то, что происходит,
// когда на них никто не отвечает.
func pendingService(t *testing.T) (*Service, context.Context) {
	t.Helper()

	obj := layout.Object{Site: "obj-17", Rooms: []layout.Room{{
		ID:   "room-1",
		Name: "Теплокамера",
		Equipment: []layout.UnitSpec{
			{Unit: "light"},
			{Unit: "fan"},
			{Unit: layout.UnitSound},
		},
	}}}

	svc := New(stubLayout{obj: obj}, repo.NewMemory(), repo.NewStates(),
		repo.NewSessions(), stubStreams{}, nil, nil, nil, nil, nil,
		repo.NewAlarms(), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.WithUnits(func(context.Context, layout.Room, string, bool, string) error {
		return nil
	})
	return svc, context.Background()
}

// Ожидание не должно висеть вечно: ответ сборщика теряется, а узел,
// оставшийся «ждущим», переставал бы слушаться пульта.
func TestОжиданиеСнимаетсяПоСроку(t *testing.T) {
	svc, ctx := pendingService(t)

	if _, err := svc.SetUnit(ctx, "room-1", "light", true, "оператор"); err != nil {
		t.Fatal(err)
	}

	st, err := svc.State(ctx, "room-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, waiting := st.Pending["light"]; !waiting {
		t.Fatal("узел не встал в ожидание")
	}

	// Срок ещё не вышел — ожидание держится.
	svc.stepPending(ctx)
	st, _ = svc.State(ctx, "room-1")
	if _, waiting := st.Pending["light"]; !waiting {
		t.Error("ожидание снято раньше срока")
	}

	// Отводим время назад: ждать пятнадцать секунд в тесте незачем.
	st.PendingSince["light"] = time.Now().Add(-PendingTTL - time.Second)
	if err := svc.states.Save(ctx, st); err != nil {
		t.Fatal(err)
	}

	svc.stepPending(ctx)
	st, _ = svc.State(ctx, "room-1")
	if _, waiting := st.Pending["light"]; waiting {
		t.Error("ожидание не снято по сроку")
	}
	if st.Equipment["light"] {
		t.Error("неподтверждённый узел показан включённым")
	}
	if _, ok := st.PendingSince["light"]; ok {
		t.Error("отметка времени осталась после снятия ожидания")
	}
}

// Снятое ожидание возвращает узел к последнему подтверждённому
// состоянию, а не гасит его: выключать работающий свет из-за
// потерянного пакета нельзя.
func TestСнятиеОжиданияНеТеряетПодтверждённое(t *testing.T) {
	svc, ctx := pendingService(t)

	if _, err := svc.SetUnit(ctx, "room-1", "light", true, "оператор"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Confirm(ctx, "room-1", "light", true, true, ""); err != nil {
		t.Fatal(err)
	}

	// Вторая команда — выключить — остаётся без ответа.
	if _, err := svc.SetUnit(ctx, "room-1", "light", false, "оператор"); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.State(ctx, "room-1")
	st.PendingSince["light"] = time.Now().Add(-PendingTTL - time.Second)
	if err := svc.states.Save(ctx, st); err != nil {
		t.Fatal(err)
	}

	svc.stepPending(ctx)
	st, _ = svc.State(ctx, "room-1")
	if _, waiting := st.Pending["light"]; waiting {
		t.Fatal("ожидание не снято")
	}
	if !st.Equipment["light"] {
		t.Error("подтверждённое включение потеряно вместе с ожиданием")
	}
}

// Подтверждение снимает ожидание сразу, не дожидаясь срока.
func TestПодтверждениеСнимаетОжидание(t *testing.T) {
	svc, ctx := pendingService(t)

	if _, err := svc.SetUnit(ctx, "room-1", "fan", true, "оператор"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Confirm(ctx, "room-1", "fan", true, true, ""); err != nil {
		t.Fatal(err)
	}

	st, _ := svc.State(ctx, "room-1")
	if _, waiting := st.Pending["fan"]; waiting {
		t.Error("ожидание осталось после подтверждения")
	}
	if _, ok := st.PendingSince["fan"]; ok {
		t.Error("отметка времени осталась после подтверждения")
	}
	if !st.Equipment["fan"] {
		t.Error("подтверждённый узел не включён")
	}
}

// Ожидание, пришедшее от прежней версии центра, без отметки времени:
// срок должен пойти с этого момента, а не истечь немедленно.
func TestОжиданиеБезОтметкиПолучаетСрок(t *testing.T) {
	svc, ctx := pendingService(t)

	st, err := svc.State(ctx, "room-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Pending = map[string]bool{"light": true}
	st.PendingSince = nil
	if err := svc.states.Save(ctx, st); err != nil {
		t.Fatal(err)
	}

	svc.stepPending(ctx)
	st, _ = svc.State(ctx, "room-1")
	if _, waiting := st.Pending["light"]; !waiting {
		t.Fatal("ожидание без отметки снято немедленно")
	}
	if _, ok := st.PendingSince["light"]; !ok {
		t.Error("отметка времени не поставлена")
	}
}

// Подтверждения и автоматика работают из разных горутин. Ни одно
// подтверждение не должно затеряться: раньше сохранивший последним
// затирал чужую запись целиком.
func TestПараллельныеПодтвержденияНеТеряются(t *testing.T) {
	svc, ctx := pendingService(t)

	units := []string{"light", "fan"}
	for _, u := range units {
		if _, err := svc.SetUnit(ctx, "room-1", u, true, "оператор"); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			svc.stepPending(ctx)
			svc.touchUnit(ctx, "room-1", "light")
		}
	}()
	for _, u := range units {
		if err := svc.Confirm(ctx, "room-1", u, true, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	<-done

	st, _ := svc.State(ctx, "room-1")
	for _, u := range units {
		if !st.Equipment[u] {
			t.Errorf("подтверждение узла %s затёрто", u)
		}
		if _, waiting := st.Pending[u]; waiting {
			t.Errorf("узел %s остался в ожидании после подтверждения", u)
		}
	}
}

// Звук крутит центр: узел подтверждается сразу, иначе ожидание
// истекало и выключить звук с пульта было невозможно.
func TestЗвукПодтверждаетсяСразу(t *testing.T) {
	svc, ctx := pendingService(t)

	if _, err := svc.SetUnit(ctx, "room-1", layout.UnitSound, true, "пульт"); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.State(ctx, "room-1")
	if !st.Equipment[layout.UnitSound] {
		t.Fatal("звук не считается включённым")
	}
	if _, waiting := st.Pending[layout.UnitSound]; waiting {
		t.Error("звук ждёт подтверждения, которое не придёт")
	}

	if _, err := svc.SetUnit(ctx, "room-1", layout.UnitSound, false, "пульт"); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.State(ctx, "room-1")
	if st.Equipment[layout.UnitSound] {
		t.Error("звук не выключился")
	}
}
