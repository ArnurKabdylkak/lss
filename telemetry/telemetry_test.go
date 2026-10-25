package telemetry

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		in   Telemetry
		ok   bool
	}{
		{"показания датчика", Telemetry{RPM: 1500, TempC: 23.5}, true},
		{"нажатие клавиши", Telemetry{Event: EventKey, Key: "5"}, true},
		{"клавиша без символа", Telemetry{Event: EventKey}, false},
		{"событие реле", Telemetry{Event: EventRelay, State: "off", Reason: "overtemp"}, true},
		{"реле без состояния", Telemetry{Event: EventRelay, Reason: "overtemp"}, false},
		{"событие камеры", Telemetry{Event: EventCamera, Camera: "cam-1"}, true},
		{"камера без имени", Telemetry{Event: EventCamera}, false},
		{"неизвестное событие", Telemetry{Event: "нечто"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.ok && err != nil {
				t.Fatalf("ожидалось «годен», получено %v", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалось ErrInvalid, получено %v", err)
			}
		})
	}
}

// Формат на проводе задаёт прошивка; расхождение здесь означает, что
// центр перестанет понимать платы.
func TestDecodeDatagramМatchesFirmware(t *testing.T) {
	raw := []byte(`{"rpm":1500.0,"period_us":40000,"pulses":12345,` +
		`"temp_c":23.50,"press_pa":101325,"uptime_ms":123456}`)

	got, err := DecodeDatagram(raw)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if !got.IsSensorReading() {
		t.Error("показания датчика распознаны как событие")
	}
	if got.RPM != 1500 || got.Pulses != 12345 || got.TempC != 23.5 || got.UptimeMS != 123456 {
		t.Errorf("поля разобраны неверно: %+v", got)
	}
}

func TestDecodeRelayEvent(t *testing.T) {
	raw := []byte(`{"event":"relay","state":"off","reason":"overtemp",` +
		`"temp_c":41.20,"limit_c":40.00,"uptime_ms":123700}`)

	got, err := DecodeDatagram(raw)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if !got.IsRelayEvent() || got.State != "off" || got.Reason != "overtemp" {
		t.Errorf("событие реле разобрано неверно: %+v", got)
	}
	if !got.IsEvent() {
		t.Error("событие реле должно считаться событием")
	}
}

func TestRebooted(t *testing.T) {
	prev := Packet{Data: Telemetry{UptimeMS: 5000}}
	next := Packet{Data: Telemetry{UptimeMS: 120}}

	if !next.Rebooted(prev) {
		t.Error("падение uptime не распознано как перезапуск")
	}
	if prev.Rebooted(prev) {
		t.Error("одинаковый uptime не должен считаться перезапуском")
	}
}

// Черновик контракта новых тренажёров: пока прошивка их не шлёт,
// тест фиксирует формат, по которому пойдёт согласование.
func TestStairsAndLoadDraft(t *testing.T) {
	t.Parallel()

	floor := Telemetry{Event: EventFloor, Floor: 7, Floors: 12, FloorDir: "up", StepCount: 204}
	if !floor.IsFloorEvent() || floor.IsSetEvent() {
		t.Error("событие этажа определено неверно")
	}
	if err := floor.Validate(); err != nil {
		t.Errorf("корректное событие этажа отклонено: %v", err)
	}

	// Направление обязательно: без него по событию не понять,
	// засчитывать подъём или спуск.
	noDir := Telemetry{Event: EventFloor, Floor: 7}
	if err := noDir.Validate(); err == nil {
		t.Error("событие этажа без направления должно отклоняться")
	}

	set := Telemetry{Event: EventSet, LoadKG: 40, Reps: 12, Sets: 3}
	if !set.IsSetEvent() || set.IsFloorEvent() {
		t.Error("событие подхода определено неверно")
	}
	if err := set.Validate(); err != nil {
		t.Errorf("корректное событие подхода отклонено: %v", err)
	}
}
