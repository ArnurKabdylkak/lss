package training

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

type stubLayout struct{ obj layout.Object }

func (s stubLayout) Object(context.Context) (layout.Object, error) { return s.obj, nil }

func object() layout.Object {
	return layout.Object{Rooms: []layout.Room{{
		ID: "room-1",
		Devices: []layout.Device{
			{ID: "bike-1", Kind: layout.KindBike},
			{ID: "stairs-1", Kind: layout.KindStairs, StepsPerFloor: 16},
			{ID: "load-1", Kind: layout.KindLoad},
		},
		Heater: layout.Heater{Device: "heater-1", LimitC: 60, ResumeC: 50},
	}}}
}

func save(t *testing.T, state *repo.Memory, id string, data telemetry.Telemetry) {
	t.Helper()

	err := state.Save(context.Background(), telemetry.Packet{
		Device: telemetry.DeviceID(id), Received: time.Now(), Data: data,
	})
	if err != nil {
		t.Fatalf("подготовка состояния: %v", err)
	}
}

// stubCards — картотека, в которой заведены перечисленные карточки.
type stubCards struct {
	known map[string]bool
	names map[string]rescuer.Name
}

func (c stubCards) Resolve(_ context.Context, iins []rescuer.IIN) ([]rescuer.Card, error) {
	out := make([]rescuer.Card, 0, len(iins))
	for _, i := range iins {
		if !c.known[string(i)] {
			return nil, fmt.Errorf("%w: %s", rescuer.ErrNotFound, i)
		}
		out = append(out, rescuer.Card{IIN: i, Name: c.names[string(i)]})
	}
	return out, nil
}

func service(state *repo.Memory, known ...string) *Service {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cards := stubCards{
		known: make(map[string]bool, len(known)),
		names: map[string]rescuer.Name{
			"900315501234": "Иванов Пётр Сергеевич",
			"сл-17":        "Петров Иван",
		},
	}
	for _, k := range known {
		cards.known[k] = true
	}
	return New(stubLayout{obj: object()}, repo.NewSessions(), state, cards, nil, nil, log)
}

func TestStartAndFinish(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state)

	// Счётчики на момент начала: прошлые заезды их не обнуляли.
	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 800})
	save(t, state, "stairs-1", telemetry.Telemetry{Floors: 12, StepCount: 300})
	save(t, state, "load-1", telemetry.Telemetry{Sets: 4, LoadKG: 40})

	sess, err := svc.Start(ctx, "room-1", StartOptions{Examinee: "  Иванов   Пётр Сергеевич ", Rescuers: nil}, "Дежурный")
	if err != nil {
		t.Fatalf("Start() вернул ошибку: %v", err)
	}
	if sess.Examinee != "Иванов Пётр Сергеевич" {
		t.Errorf("ФИО не нормализовано: %q", sess.Examinee)
	}
	if !sess.Active() || sess.Operator != "Дежурный" {
		t.Errorf("занятие открыто неверно: %+v", sess)
	}

	// Второе занятие в той же комнате открыть нельзя.
	if _, err := svc.Start(ctx, "room-1", StartOptions{Examinee: "Петров Иван", Rescuers: nil}, "Дежурный"); !errors.Is(err, training.ErrBusy) {
		t.Errorf("повторное открытие = %v, ожидалось ErrBusy", err)
	}

	// Заезд: счётчики выросли.
	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 2300})
	save(t, state, "stairs-1", telemetry.Telemetry{Floors: 20, StepCount: 540})
	save(t, state, "load-1", telemetry.Telemetry{Sets: 9, LoadKG: 52.5})

	done, err := svc.Finish(ctx, "room-1")
	if err != nil {
		t.Fatalf("Finish() вернул ошибку: %v", err)
	}

	r := done.Result()
	if r.DistanceM != 1500 || r.Floors != 8 || r.Steps != 240 || r.Sets != 5 {
		t.Errorf("результат = %+v", r)
	}
	if r.MaxLoadKG != 52.5 {
		t.Errorf("наибольший вес = %v, ожидалось 52.5", r.MaxLoadKG)
	}

	// Комната освободилась, занятие попало в журнал.
	if _, ok, _ := svc.Active(ctx, "room-1"); ok {
		t.Error("после завершения комната должна быть свободна")
	}
	recent, err := svc.Recent(ctx, "room-1")
	if err != nil || len(recent) != 1 || recent[0].Examinee != "Иванов Пётр Сергеевич" {
		t.Errorf("журнал занятий = %+v, err %v", recent, err)
	}
}

func TestStartRejects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := service(repo.NewMemory())

	if _, err := svc.Start(ctx, "room-1", StartOptions{Examinee: "   ", Rescuers: nil}, "Дежурный"); !errors.Is(err, training.ErrNoExaminee) {
		t.Errorf("пустое ФИО = %v, ожидалось ErrNoExaminee", err)
	}
	if _, err := svc.Start(ctx, "room-1", StartOptions{Examinee: "Иванов 1995", Rescuers: nil}, "Дежурный"); !errors.Is(err, training.ErrBadExaminee) {
		t.Errorf("ФИО с цифрами = %v, ожидалось ErrBadExaminee", err)
	}
	if _, err := svc.Start(ctx, "room-9", StartOptions{Examinee: "Иванов Пётр", Rescuers: nil}, "Дежурный"); err == nil {
		t.Error("занятие в неизвестной комнате должно отклоняться")
	}
	if _, err := svc.Finish(ctx, "room-1"); !errors.Is(err, training.ErrNoSession) {
		t.Errorf("завершение без занятия = %v, ожидалось ErrNoSession", err)
	}
}

func TestStartWithCards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state, "900315501234", "сл-17")

	// Занятие звеном: показания закрепляются за обеими карточками.
	sess, err := svc.Start(ctx, "room-1", StartOptions{Examinee: "", Rescuers: []string{"900315 501 234", "сл-17"}}, "Дежурный")
	if err != nil {
		t.Fatalf("Start() вернул ошибку: %v", err)
	}
	if len(sess.Rescuers) != 2 {
		t.Fatalf("участников = %v, ожидалось двое", sess.Rescuers)
	}
	// ИИН нормализован: пробелы в номере не создают третью карточку.
	if sess.Rescuers[0] != "900315501234" {
		t.Errorf("ИИН не нормализован: %q", sess.Rescuers[0])
	}
	// Имя занятия собрано из карточек, а не из пустого поля ввода.
	if sess.Examinee != "Иванов Пётр Сергеевич, Петров Иван" {
		t.Errorf("имя занятия = %q, ожидался перечень из карточек", sess.Examinee)
	}
	if !sess.HasRescuer("сл-17") {
		t.Error("вторая карточка не записана в занятие")
	}

	if _, err := svc.Finish(ctx, "room-1"); err != nil {
		t.Fatalf("Finish() вернул ошибку: %v", err)
	}

	// Карточки, которой нет в картотеке, быть не должно.
	_, err = svc.Start(ctx, "room-1", StartOptions{Examinee: "", Rescuers: []string{"нет-такой"}}, "Дежурный")
	if !errors.Is(err, rescuer.ErrNotFound) {
		t.Errorf("назначение неизвестной карточки = %v, ожидалось ErrNotFound", err)
	}
}

func TestAssignments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state, "900315501234", "сл-17")

	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 100})
	save(t, state, "stairs-1", telemetry.Telemetry{Floors: 5, StepCount: 120})

	// Назначения при открытии: каждый на своём тренажёре.
	sess, err := svc.Start(ctx, "room-1", StartOptions{
		Team:     "Звено 1",
		Rescuers: []string{"900315501234", "сл-17"},
		Assignments: []Assignment{
			{Device: "bike-1", IIN: "900315501234"},
			{Device: "stairs-1", IIN: "сл-17"},
		},
	}, "Дежурный")
	if err != nil {
		t.Fatalf("Start() вернул ошибку: %v", err)
	}
	if len(sess.Current()) != 2 {
		t.Fatalf("действующих назначений = %d, ожидалось 2", len(sess.Current()))
	}
	if d, ok := sess.Occupies("900315501234"); !ok || d != "bike-1" {
		t.Errorf("карточка не на своём тренажёре: %q", d)
	}

	// Один человек — один тренажёр: иначе показания считались бы дважды.
	if _, err := svc.Assign(ctx, "room-1", "stairs-1", "900315501234"); !errors.Is(err, training.ErrNotAssigned) {
		t.Errorf("второй тренажёр одному человеку = %v, ожидалось ErrNotAssigned", err)
	}

	// Назначать можно только того, кто в составе занятия.
	if _, err := svc.Assign(ctx, "room-1", "bike-1", "чужой"); !errors.Is(err, training.ErrNotAssigned) {
		t.Errorf("чужая карточка = %v, ожидалось ErrNotAssigned", err)
	}

	// Смена тренажёра: прежнее упражнение закрывается, результат
	// остаётся за прежним участником.
	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 1600})
	after, err := svc.Assign(ctx, "room-1", "bike-1", "")
	if err != nil {
		t.Fatalf("снятие с тренажёра вернуло ошибку: %v", err)
	}
	var closed *training.Assignment
	for i := range after.Assignments {
		if a := &after.Assignments[i]; a.Device == "bike-1" && !a.Running() {
			closed = a
		}
	}
	if closed == nil {
		t.Fatal("упражнение не закрыто")
	}
	if got := closed.Result().DistanceM; got != 1500 {
		t.Errorf("результат упражнения = %v м, ожидалось 1500", got)
	}
	if closed.IIN != "900315501234" {
		t.Errorf("результат закреплён не за тем: %q", closed.IIN)
	}
}

func TestCapacityAndTrainerOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := service(repo.NewMemory(), "a", "b", "c", "d")

	// В комнату помещается трое (умолчание сценария).
	_, err := svc.Start(ctx, "room-1", StartOptions{Examinee: "", Rescuers: []string{"a", "b", "c", "d"}}, "Дежурный")
	if !errors.Is(err, training.ErrCapacity) {
		t.Errorf("превышение вместимости = %v, ожидалось ErrCapacity", err)
	}

	// Поимённо назначают только на тренажёры: пушка им не является.
	_, err = svc.Start(ctx, "room-1", StartOptions{
		Rescuers:    []string{"a"},
		Assignments: []Assignment{{Device: "heater-1", IIN: "a"}},
	}, "Дежурный")
	if !errors.Is(err, training.ErrNotAssigned) {
		t.Errorf("назначение на пушку = %v, ожидалось ErrNotAssigned", err)
	}
}
