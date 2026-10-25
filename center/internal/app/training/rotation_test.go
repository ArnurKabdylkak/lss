package training

import (
	"context"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// seats — кто на каком тренажёре сейчас.
func seats(t *testing.T, svc *Service) map[string]string {
	t.Helper()

	ex, err := svc.Exercises(context.Background(), "room-1")
	if err != nil {
		t.Fatalf("упражнения не прочитаны: %v", err)
	}
	out := map[string]string{}
	for _, e := range ex {
		if e.Busy() {
			out[e.Device.ID] = e.IIN
		}
	}
	return out
}

// Звено с тремя людьми идёт по кругу: выполнив задание, каждый
// переходит на следующий тренажёр, а его место занимает товарищ.
func TestЗвеноМеняетсяТренажёрамиПоКругу(t *testing.T) {
	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state, "900315501234", "сл-17", "сл-18")

	save(t, state, "bike-1", telemetry.Telemetry{DistanceM: 0})
	save(t, state, "stairs-1", telemetry.Telemetry{Floors: 0, StepCount: 0})
	save(t, state, "load-1", telemetry.Telemetry{Sets: 0})

	_, err := svc.Start(ctx, "room-1", StartOptions{
		Rescuers: []string{"900315501234", "сл-17", "сл-18"},
		Assignments: []Assignment{
			{Device: "bike-1", IIN: "900315501234"},
			{Device: "stairs-1", IIN: "сл-17"},
			{Device: "load-1", IIN: "сл-18"},
		},
	}, "оператор")
	if err != nil {
		t.Fatalf("занятие не открыто: %v", err)
	}

	// Задание у всех троих — секунда времени: столько же, сколько в
	// зале, только короче.
	for _, dev := range []string{"bike-1", "stairs-1", "load-1"} {
		if _, err := svc.SetTarget(ctx, "room-1", dev,
			training.Target{Duration: 50 * time.Millisecond}); err != nil {
			t.Fatalf("задание не задано: %v", err)
		}
	}

	before := seats(t, svc)
	if len(before) != 3 {
		t.Fatalf("занято тренажёров %d, ожидалось 3", len(before))
	}

	// Пока срок не вышел, круг стоит на месте.
	svc.stepRotation(ctx)
	if got := seats(t, svc); got["bike-1"] != before["bike-1"] {
		t.Error("смена произошла до выполнения задания")
	}

	time.Sleep(60 * time.Millisecond)
	svc.stepRotation(ctx)

	after := seats(t, svc)
	if len(after) != 3 {
		t.Fatalf("после смены занято тренажёров %d", len(after))
	}

	// Сдвиг ровно на один: кто был на велосипеде, ушёл на лестницу.
	if after["stairs-1"] != before["bike-1"] {
		t.Errorf("с велосипеда ушли не на лестницу: было %v, стало %v",
			before, after)
	}
	if after["load-1"] != before["stairs-1"] {
		t.Errorf("с лестницы ушли не на молот: было %v, стало %v", before, after)
	}
	if after["bike-1"] != before["load-1"] {
		t.Errorf("круг не замкнулся: было %v, стало %v", before, after)
	}

	// Никто не остался на двух тренажёрах разом: иначе его показания
	// считались бы дважды.
	seen := map[string]bool{}
	for _, iin := range after {
		if seen[iin] {
			t.Errorf("карточка %s занимает два тренажёра", iin)
		}
		seen[iin] = true
	}

	// Сразу после смены отсчёт идёт заново — второй круг не
	// проворачивается в ту же секунду.
	svc.stepRotation(ctx)
	if got := seats(t, svc); got["bike-1"] != after["bike-1"] {
		t.Error("круг провернулся дважды подряд")
	}
}

// Одному человеку меняться не с кем: круг не двигают.
func TestОдинЧеловекНеМеняется(t *testing.T) {
	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state, "900315501234")

	save(t, state, "bike-1", telemetry.Telemetry{})
	_, err := svc.Start(ctx, "room-1", StartOptions{
		Rescuers:    []string{"900315501234"},
		Assignments: []Assignment{{Device: "bike-1", IIN: "900315501234"}},
	}, "оператор")
	if err != nil {
		t.Fatalf("занятие не открыто: %v", err)
	}
	if _, err := svc.SetTarget(ctx, "room-1", "bike-1",
		training.Target{Duration: 10 * time.Millisecond}); err != nil {
		t.Fatalf("задание не задано: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	svc.stepRotation(ctx)

	if got := seats(t, svc)["bike-1"]; got != "900315501234" {
		t.Errorf("одиночку пересадили: %q", got)
	}
}

// Пока хотя бы один не выполнил задание, круг стоит: иначе человек
// пришёл бы на занятый тренажёр.
func TestКругЖдётОтстающего(t *testing.T) {
	ctx := context.Background()
	state := repo.NewMemory()
	svc := service(state, "900315501234", "сл-17")

	save(t, state, "bike-1", telemetry.Telemetry{})
	save(t, state, "stairs-1", telemetry.Telemetry{})

	_, err := svc.Start(ctx, "room-1", StartOptions{
		Rescuers: []string{"900315501234", "сл-17"},
		Assignments: []Assignment{
			{Device: "bike-1", IIN: "900315501234"},
			{Device: "stairs-1", IIN: "сл-17"},
		},
	}, "оператор")
	if err != nil {
		t.Fatalf("занятие не открыто: %v", err)
	}

	// Велосипедист укладывается быстро, лестница — нет.
	if _, err := svc.SetTarget(ctx, "room-1", "bike-1",
		training.Target{Duration: 10 * time.Millisecond}); err != nil {
		t.Fatalf("задание не задано: %v", err)
	}
	if _, err := svc.SetTarget(ctx, "room-1", "stairs-1",
		training.Target{Duration: time.Hour}); err != nil {
		t.Fatalf("задание не задано: %v", err)
	}

	before := seats(t, svc)
	time.Sleep(20 * time.Millisecond)
	svc.stepRotation(ctx)

	if got := seats(t, svc); got["bike-1"] != before["bike-1"] {
		t.Error("круг провернулся, не дождавшись отстающего")
	}
}
