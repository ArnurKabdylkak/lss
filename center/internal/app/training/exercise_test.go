package training

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

func TestExercisesPerRescuer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state, "900315501234", "сл-17")

	// Счётчики накопительные: до занятия на них уже что-то есть.
	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 5000})
	save(t, state, "stairs-1", telemetry.Telemetry{StepCount: 800, Floors: 50})

	_, err := svc.Start(ctx, "room-1", StartOptions{
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

	// Велоэргометр и лестница зачитываются по времени: спасатель
	// работает отведённый срок, метры и этажи — для наблюдения.
	if _, err := svc.SetTarget(ctx, "room-1", "bike-1",
		training.Target{Duration: time.Minute}); err != nil {
		t.Fatalf("SetTarget() вернул ошибку: %v", err)
	}
	if _, err := svc.SetTarget(ctx, "room-1", "stairs-1",
		training.Target{Duration: 30 * time.Second}); err != nil {
		t.Fatalf("SetTarget() вернул ошибку: %v", err)
	}

	// Задание в подъёмах этим тренажёрам не ставится.
	if _, err := svc.SetTarget(ctx, "room-1", "bike-1",
		training.Target{Reps: 20}); !errors.Is(err, training.ErrNotAssigned) {
		t.Errorf("подъёмы велоэргометру = %v, ожидалось ErrNotAssigned", err)
	}

	// Поехали: показатели считаются как прирост от назначения.
	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 5600})
	save(t, state, "stairs-1", telemetry.Telemetry{StepCount: 976, Floors: 61})

	ex, err := svc.Exercises(ctx, "room-1")
	if err != nil {
		t.Fatalf("Exercises() вернул ошибку: %v", err)
	}
	byDevice := make(map[string]Exercise, len(ex))
	for _, e := range ex {
		byDevice[e.Device.ID] = e
	}

	bike := byDevice["bike-1"]
	if bike.IIN != "900315501234" {
		t.Errorf("велоэргометр закреплён не за тем: %q", bike.IIN)
	}
	if bike.Progress.DistanceM != 600 {
		t.Errorf("дистанция = %v м, ожидалось 600 (прирост, а не счётчик)", bike.Progress.DistanceM)
	}
	// Срок не вышел — задание не выполнено, сколько бы метров ни было.
	if bike.Done {
		t.Error("минута не прошла, зачёта быть не может")
	}
	if bike.Left <= 0 || bike.Left > time.Minute {
		t.Errorf("осталось = %s, ожидалось около минуты", bike.Left)
	}

	// Лестница: этажи считаются по правилу из настроек — 16 ступеней
	// на этаж, а не по тому, что насчитала плата.
	stairs := byDevice["stairs-1"]
	if stairs.Progress.Steps != 176 {
		t.Errorf("ступеней = %d, ожидалось 176", stairs.Progress.Steps)
	}
	if stairs.Progress.Floors != 11 {
		t.Errorf("этажей = %d, ожидалось 11 (176 / 16): показываются для наблюдения", stairs.Progress.Floors)
	}
	if stairs.Done {
		t.Error("полминуты не прошло, зачёта быть не может")
	}

	// Смена участника: прежний результат остаётся при нём, новый
	// отсчёт начинается с нуля.
	if _, err := svc.Assign(ctx, "room-1", "bike-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Assign(ctx, "room-1", "bike-1", "сл-17"); err == nil {
		t.Error("сл-17 уже на лестнице, второй тренажёр ему нельзя")
	}

	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 5600})
	if _, err := svc.Assign(ctx, "room-1", "stairs-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Assign(ctx, "room-1", "bike-1", "сл-17"); err != nil {
		t.Fatalf("смена тренажёра отклонена: %v", err)
	}

	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 5750})
	ex, _ = svc.Exercises(ctx, "room-1")
	for _, e := range ex {
		if e.Device.ID != "bike-1" {
			continue
		}
		if e.IIN != "сл-17" {
			t.Errorf("велоэргометр закреплён за %q", e.IIN)
		}
		if e.Progress.DistanceM != 150 {
			t.Errorf("новый отсчёт = %v м, ожидалось 150", e.Progress.DistanceM)
		}
	}

	// Задание осталось при тренажёре, а не ушло с человеком.
	sess, _, _ := svc.Active(ctx, "room-1")
	if sess.Target("bike-1").Duration != time.Minute {
		t.Error("задание должно оставаться при тренажёре")
	}
}

func TestTargetDone(t *testing.T) {
	t.Parallel()

	// Срок: зачёт по отработанному времени, метры к нему отношения
	// не имеют.
	byTime := training.Target{Duration: time.Minute}
	if !byTime.Done(training.Counters{}, 61*time.Second) {
		t.Error("минута отработана — задание выполнено")
	}
	if byTime.Done(training.Counters{DistanceM: 100000}, 59*time.Second) {
		t.Error("срок не вышел: зачёта быть не может, сколько бы ни намотали")
	}
	if left := byTime.Left(20 * time.Second); left != 40*time.Second {
		t.Errorf("осталось = %s, ожидалось 40 с", left)
	}
	if left := byTime.Left(2 * time.Minute); left != 0 {
		t.Errorf("таймер ушёл в минус: %s", left)
	}

	// Подъёмы: зачёт у ударного молота.
	byReps := training.Target{Reps: 20}
	if !byReps.Done(training.Counters{Sets: 20}, 0) {
		t.Error("20 подъёмов закрывают задание")
	}
	if byReps.Done(training.Counters{Sets: 19}, time.Hour) {
		t.Error("19 подъёмов задание не закрывают")
	}

	if (training.Target{}).Done(training.Counters{Sets: 100}, time.Hour) {
		t.Error("без задания зачёта быть не может")
	}
}

// stubHistory отдаёт заданные показания — как история телеметрии.
type stubHistory struct{ packets []telemetry.Packet }

func (h stubHistory) History(context.Context, port.HistoryQuery) ([]telemetry.Packet, error) {
	return h.packets, nil
}

func TestTemperatureOverSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := repo.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// История: температура росла, есть и пакет без неё — событие реле.
	history := stubHistory{packets: []telemetry.Packet{
		{Data: telemetry.Telemetry{TempC: 38}},
		{Data: telemetry.Telemetry{TempC: 42}},
		{Data: telemetry.Telemetry{Event: telemetry.EventRelay, State: "off"}},
		{Data: telemetry.Telemetry{TempC: 40}},
	}}

	cards := stubCards{known: map[string]bool{"т-1": true},
		names: map[string]rescuer.Name{"т-1": "Тестов Иван"}}
	svc := New(stubLayout{obj: object()}, repo.NewSessions(), state, cards, nil, history, log)

	if _, err := svc.Start(ctx, "room-1",
		StartOptions{Team: "Смена 2", Rescuers: []string{"т-1"}}, "Дежурный"); err != nil {
		t.Fatalf("Start() вернул ошибку: %v", err)
	}

	done, err := svc.Finish(ctx, "room-1")
	if err != nil {
		t.Fatalf("Finish() вернул ошибку: %v", err)
	}

	if !done.Temp.Known() {
		t.Fatal("температура за занятие не посчитана")
	}
	if done.Temp.Samples != 3 {
		t.Errorf("показаний = %d, ожидалось 3: пакет без температуры в счёт не идёт", done.Temp.Samples)
	}
	if done.Temp.MinC != 38 || done.Temp.MaxC != 42 {
		t.Errorf("диапазон = %.0f…%.0f, ожидалось 38…42", done.Temp.MinC, done.Temp.MaxC)
	}
	if done.Temp.AvgC != 40 {
		t.Errorf("среднее = %.1f, ожидалось 40", done.Temp.AvgC)
	}

	// Комментарий пишется после занятия и правится.
	commented, err := svc.Comment(ctx, string(done.ID), "  норматив сдан  ", "Дежурный")
	if err != nil {
		t.Fatalf("Comment() вернул ошибку: %v", err)
	}
	if commented.Comment != "норматив сдан" {
		t.Errorf("комментарий = %q", commented.Comment)
	}
	if _, err := svc.Comment(ctx, "нет-такого", "текст", "Дежурный"); !errors.Is(err, training.ErrNoSession) {
		t.Errorf("комментарий к несуществующему занятию = %v", err)
	}
}

// Итог теплокамеры считается по заданиям тренажёрам: маршрута там
// нет, и раньше любое занятие закрывалось как «Не завершена».
func TestTargetsMet(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-10 * time.Minute)
	end := start.Add(5 * time.Minute)

	// Норма подъёмов задана и выполнена по показаниям платы.
	sess := training.Session{
		Targets: map[string]training.Target{"молот": {Reps: 5}},
		Assignments: []training.Assignment{{
			Device: "молот", IIN: "1", Since: start, Until: end,
			AtStart: training.Counters{Sets: 100},
			AtEnd:   training.Counters{Sets: 107},
		}},
	}
	if set, met := targetsMet(sess); !set || !met {
		t.Errorf("задание выполнено: set=%v met=%v", set, met)
	}

	// Не добрали — зачёта нет.
	sess.Assignments[0].AtEnd = training.Counters{Sets: 103}
	if set, met := targetsMet(sess); !set || met {
		t.Errorf("норма не добрана: set=%v met=%v", set, met)
	}

	// Заданий не было вовсе — считать нечего.
	if set, _ := targetsMet(training.Session{}); set {
		t.Error("без заданий зачётных условий нет")
	}

	// Срок набирается по всем назначениям: подмена спасателя не
	// обнуляет отработанное время.
	byTime := training.Session{
		Targets: map[string]training.Target{"вело": {Duration: 5 * time.Minute}},
		Assignments: []training.Assignment{
			{Device: "вело", IIN: "1", Since: start, Until: start.Add(4 * time.Minute)},
			{Device: "вело", IIN: "2", Since: start.Add(4 * time.Minute), Until: end},
		},
	}
	if set, met := targetsMet(byTime); !set || !met {
		t.Errorf("общий срок выработан: set=%v met=%v", set, met)
	}
}
