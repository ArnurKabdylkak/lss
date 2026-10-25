package app

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// Реле-заглушка: запоминает положение, как настоящее.
type fakeSwitch struct {
	on      bool
	changes int
	fail    error
}

func (f *fakeSwitch) Set(on bool) error {
	if f.fail != nil {
		return f.fail
	}
	if f.on != on {
		f.changes++
	}
	f.on = on
	return nil
}

func (f *fakeSwitch) Value() bool { return f.on }

func newHeater(t *testing.T) (*Heater, *fakeSwitch) {
	t.Helper()

	sw := &fakeSwitch{}
	h := NewHeater(sw, HeaterLimits{
		LimitC:    60,
		RestoreC:  50,
		MinOff:    0, // паузу проверяем отдельным тестом
		BoardTTL:  5 * time.Second,
		DisagreeC: 5,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	return h, sw
}

// До команды инструктора тренажёр не греет.
func TestПушкаСтартуетВыключенной(t *testing.T) {
	h, sw := newHeater(t)

	if sw.on {
		t.Error("реле замкнуто сразу после запуска")
	}
	if st := h.State(); st.On || st.Allowed || st.Reason != HeaterOperatorOff {
		t.Errorf("неожиданное состояние: %+v", st)
	}
}

// Без достоверной температуры защита работать не может.
func TestБезДатчиковНеВключается(t *testing.T) {
	h, sw := newHeater(t)

	st := h.Allow(true)
	if st.On || sw.on {
		t.Error("пушка включена без единого датчика")
	}
	if st.Reason != HeaterSensorFail {
		t.Errorf("причина %q, ожидалась sensor_fail", st.Reason)
	}
}

func TestВключаетсяПриНормальнойТемпературе(t *testing.T) {
	h, sw := newHeater(t)
	h.ObserveLocal(25, time.Now())

	st := h.Allow(true)
	if !st.On || !sw.on {
		t.Fatalf("пушка не включилась: %+v", st)
	}
	if st.Reason != HeaterOK {
		t.Errorf("причина %q", st.Reason)
	}
}

// Ключевое: перегрев хотя бы по одному датчику отключает пушку.
// Среднее (25+70)/2 = 47.5 было бы ниже порога и замаскировало бы нагрев.
func TestПерегревПоЛюбомуДатчикуОтключает(t *testing.T) {
	cases := map[string]struct{ local, board float64 }{
		"перегрев на своём датчике": {local: 70, board: 25},
		"перегрев на плате":         {local: 25, board: 70},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, sw := newHeater(t)
			h.ObserveLocal(25, time.Now())
			h.Allow(true)

			h.ObserveLocal(c.local, time.Now())
			h.ObserveBoard(c.board, time.Now())
			st := h.Update()

			if st.On || sw.on {
				t.Error("пушка осталась включённой при перегреве")
			}
			if st.Reason != HeaterOvertemp {
				t.Errorf("причина %q, ожидалась overtemp", st.Reason)
			}
			if st.DecidedC == nil || *st.DecidedC != 70 {
				t.Errorf("решение принято не по худшему датчику: %v", st.DecidedC)
			}
		})
	}
}

// Устаревшие показания платы не учитываются: они идут по сети.
func TestУстаревшиеПоказанияПлатыИгнорируются(t *testing.T) {
	h, _ := newHeater(t)

	h.ObserveLocal(25, time.Now())
	h.ObserveBoard(90, time.Now().Add(-time.Minute)) // давно

	st := h.Allow(true)
	if !st.On {
		t.Error("исправный тренажёр выключен из-за устаревших данных платы")
	}
	if st.DecidedC == nil || *st.DecidedC != 25 {
		t.Errorf("решение принято по устаревшим данным: %v", st.DecidedC)
	}
}

// Потеря связи с платой не должна гасить тренажёр, если свой датчик жив.
func TestОтсутствиеПлатыНеГаситТренажёр(t *testing.T) {
	h, _ := newHeater(t)
	h.ObserveLocal(30, time.Now())

	if st := h.Allow(true); !st.On {
		t.Errorf("пушка не включилась без датчика платы: %+v", st)
	}
}

// Расхождение датчиков — признак неисправности одного из них.
func TestРасхождениеДатчиковВидно(t *testing.T) {
	h, _ := newHeater(t)

	h.ObserveLocal(25, time.Now())
	h.ObserveBoard(45, time.Now())
	st := h.Allow(true)

	if !st.Disagree {
		t.Error("расхождение в 20 °C не отмечено")
	}

	h.ObserveBoard(27, time.Now())
	if st := h.Update(); st.Disagree {
		t.Error("расхождение в 2 °C отмечено как неисправность")
	}
}

// Гистерезис: возврат только ниже температуры восстановления.
func TestГистерезис(t *testing.T) {
	h, _ := newHeater(t)
	h.ObserveLocal(25, time.Now())
	h.Allow(true)

	h.ObserveLocal(65, time.Now())
	if st := h.Update(); st.On {
		t.Fatal("пушка не отключилась при перегреве")
	}

	// Ниже порога, но выше температуры возврата — включать рано.
	h.ObserveLocal(55, time.Now())
	if st := h.Update(); st.On {
		t.Error("пушка включилась между порогом и температурой возврата")
	}

	h.ObserveLocal(45, time.Now())
	if st := h.Update(); !st.On {
		t.Error("пушка не вернулась после остывания")
	}
}

// Без выдержки реле щёлкало бы на границе.
func TestВыдержкаПослеОтключения(t *testing.T) {
	sw := &fakeSwitch{}
	h := NewHeater(sw, HeaterLimits{
		LimitC: 60, RestoreC: 50, MinOff: time.Hour,
		BoardTTL: time.Second, DisagreeC: 5,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	h.ObserveLocal(25, time.Now())
	h.Allow(true)

	h.ObserveLocal(65, time.Now())
	h.Update()

	h.ObserveLocal(20, time.Now())
	st := h.Update()

	if st.On {
		t.Error("пушка включилась до истечения выдержки")
	}
	if st.Reason != HeaterCooldown {
		t.Errorf("причина %q, ожидалась cooldown", st.Reason)
	}
}

// Запрет оператора исполняется всегда и не снимается сам.
func TestЗапретДержитсяДоОтмены(t *testing.T) {
	h, sw := newHeater(t)
	h.ObserveLocal(25, time.Now())
	h.Allow(true)

	h.Allow(false)
	if sw.on {
		t.Fatal("реле не снято по запрету оператора")
	}

	for range 10 {
		h.ObserveLocal(20, time.Now())
		if st := h.Update(); st.On {
			t.Fatal("запрет оператора снялся сам")
		}
	}

	if st := h.Allow(true); !st.On {
		t.Error("пушка не вернулась после разрешения")
	}
}

// Негодные уставки не применяются: в центре есть та же проверка, но
// полагаться на чужую валидацию узел не может.
func TestНегодныеУставкиОтклоняются(t *testing.T) {
	h, _ := newHeater(t)

	if err := h.SetLimits(60, 65); err == nil {
		t.Error("уставки без гистерезиса приняты")
	}
	if err := h.SetLimits(200, 50); err == nil {
		t.Error("порог 200 °C принят")
	}
	if err := h.SetLimits(70, 60); err != nil {
		t.Errorf("годные уставки отклонены: %v", err)
	}

	if st := h.State(); st.LimitC != 70 {
		t.Errorf("порог не применён: %v", st.LimitC)
	}
}

// Новый порог должен действовать сразу, а не со следующего опроса.
func TestСменаУставокПересчитываетСостояние(t *testing.T) {
	h, sw := newHeater(t)
	h.ObserveLocal(55, time.Now())
	h.Allow(true)

	if !sw.on {
		t.Fatal("подготовка: пушка должна работать при 55 °C и пороге 60")
	}

	if err := h.SetLimits(50, 40); err != nil {
		t.Fatalf("уставки отклонены: %v", err)
	}
	if sw.on {
		t.Error("пушка не отключилась после снижения порога ниже текущей температуры")
	}
}

// Без реле (не на RPi) защита продолжает считать — иначе разработку
// и проверку пришлось бы вести только на железе.
func TestБезРелеРаботаетВсухую(t *testing.T) {
	h := NewHeater(nil, HeaterLimits{LimitC: 60, RestoreC: 50},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	h.ObserveLocal(25, time.Now())
	st := h.Allow(true)

	if !st.Dry {
		t.Error("режим без реле не отмечен")
	}
	if !st.On {
		t.Error("состояние не считается без реле")
	}
}
