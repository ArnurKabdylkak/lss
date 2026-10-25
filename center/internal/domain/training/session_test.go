package training

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeExaminee(t *testing.T) {
	t.Parallel()

	tests := map[string]Examinee{
		"  Иванов   Пётр  Сергеевич ": "Иванов Пётр Сергеевич",
		"Петров Иван":                 "Петров Иван",
		"   ":                         "",
	}
	for in, want := range tests {
		if got := NormalizeExaminee(in); got != want {
			t.Errorf("NormalizeExaminee(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestExamineeValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      Examinee
		wantErr error
	}{
		{"обычное ФИО", "Иванов Пётр Сергеевич", nil},
		{"без отчества", "Петров Иван", nil},
		{"фамилия с дефисом", "Мамин-Сибиряк Д.", nil},
		{"пусто", "", ErrNoExaminee},
		{"цифры", "Иванов 1995", ErrBadExaminee},
		{"разметка", "Иванов <b>", ErrBadExaminee},
		{"только знаки", "- . -", ErrBadExaminee},
		{"слишком длинное", Examinee(strings.Repeat("и", MaxExamineeLen+1)), ErrBadExaminee},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.in.Validate()
			if tt.wantErr == nil && err != nil {
				t.Fatalf("Validate(%q) вернул ошибку: %v", tt.in, err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate(%q) = %v, ожидалось %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestSessionResult(t *testing.T) {
	t.Parallel()

	s := Session{
		Started:  time.Now().Add(-5 * time.Minute),
		Finished: time.Now(),
		AtStart:  Counters{DistanceM: 1000, Floors: 10, Steps: 200, Sets: 3, MaxLoadKG: 40},
		AtFinish: Counters{DistanceM: 2500, Floors: 18, Steps: 500, Sets: 8, Reps: 4, MaxLoadKG: 55},
	}

	r := s.Result()
	if r.DistanceM != 1500 || r.Floors != 8 || r.Steps != 300 || r.Sets != 5 {
		t.Errorf("результат посчитан неверно: %+v", r)
	}
	if r.MaxLoadKG != 55 {
		t.Errorf("наибольший вес = %v, ожидалось 55", r.MaxLoadKG)
	}
	if r.Reps != 4 {
		t.Errorf("повторов = %d, ожидалось 4", r.Reps)
	}

	// Перезапуск платы обнуляет счётчики: отрицательный результат
	// бессмысленнее нуля.
	rebooted := Session{
		Finished: time.Now(),
		AtStart:  Counters{DistanceM: 5000, Floors: 30},
		AtFinish: Counters{DistanceM: 120, Floors: 1},
	}
	if got := rebooted.Result(); got.DistanceM != 0 || got.Floors != 0 {
		t.Errorf("после перезапуска платы результат = %+v, ожидались нули", got)
	}

	// У идущего занятия результата ещё нет.
	if (Session{Started: time.Now()}).Result() != (Counters{}) {
		t.Error("у открытого занятия не должно быть результата")
	}
}

// Итог занятия попадает в карточку словами, и прерванное занятие
// остаётся прерванным: по этому статусу принимают зачёт (сценарий §6).
func TestSessionStatus(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		sess Session
		want string
	}{
		{"идёт", Session{Started: now}, "Идёт"},
		{"успешно", Session{Started: now, Finished: now, Outcome: OutcomeSuccess}, "Выполнена успешно"},
		{"не выполнена", Session{Started: now, Finished: now, Outcome: OutcomePartial}, "Не выполнена"},
		{"прервана", Session{Started: now, Finished: now, Outcome: OutcomeAborted, Reason: "авария"}, "Прервана"},
		{"старая запись без итога", Session{Started: now, Finished: now}, "Завершена"},
		{"старая запись с причиной", Session{Started: now, Finished: now, Reason: "авария"}, "Прервана"},
	}
	for _, c := range cases {
		if got := c.sess.Status(); got != c.want {
			t.Errorf("%s: статус %q, ожидался %q", c.name, got, c.want)
		}
	}
}

// Время прохождения задания считается от старта тренировки.
func TestTaskElapsed(t *testing.T) {
	start := time.Now()
	sess := Session{
		Started: start,
		Tasks: map[string]TaskMark{
			"maze": {Done: true, At: start.Add(90 * time.Second)},
			"code": {Done: false, At: start.Add(30 * time.Second)},
			"fire": {Done: true, At: start.Add(-time.Minute)},
		},
	}
	if got := sess.TaskElapsed("maze"); got != 90*time.Second {
		t.Errorf("maze: %v, ожидалось 1m30s", got)
	}
	if got := sess.TaskElapsed("code"); got != 0 {
		t.Errorf("непройденное задание: %v, ожидался ноль", got)
	}
	if got := sess.TaskElapsed("fire"); got != 0 {
		t.Errorf("отметка раньше старта: %v, ожидался ноль", got)
	}
	if got := sess.TaskElapsed("нет"); got != 0 {
		t.Errorf("неизвестное задание: %v, ожидался ноль", got)
	}
}

// Занятие без зачётных условий закрывается как проведённое: называть
// его незавершённым неверно — завершать было нечего.
func TestStatusDone(t *testing.T) {
	now := time.Now()
	s := Session{Started: now, Finished: now.Add(time.Minute), Outcome: OutcomeDone}
	if got := s.Status(); got != "Завершена" {
		t.Errorf("статус %q, ожидалось «Завершена»", got)
	}
}
