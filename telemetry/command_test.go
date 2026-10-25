package telemetry

import (
	"errors"
	"strings"
	"testing"
)

func TestCommandValidate(t *testing.T) {
	base := Command{ID: "a1", Device: "stm32-01"}

	cases := []struct {
		name string
		in   Command
		ok   bool
	}{
		{"выключить пушку", withKind(base, CmdHeaterOff), true},
		{"включить пушку", withKind(base, CmdHeaterOn), true},
		{"без идентификатора", Command{Device: "stm32-01", Kind: CmdHeaterOff}, false},
		{"без устройства", Command{ID: "a1", Kind: CmdHeaterOff}, false},
		{"уставки с гистерезисом", limits(base, 40, 37), true},
		{"возврат выше порога", limits(base, 40, 41), false},
		{"возврат равен порогу", limits(base, 40, 40), false},
		{"порог вне диапазона", limits(base, 500, 37), false},
		{"период в норме", period(base, 200), true},
		{"период слишком мал", period(base, 10), false},
		{"неизвестная команда", withKind(base, "перезагрузись"), false},

		{"колесо 700c", wheel(base, 2096), true},
		{"колесо детское", wheel(base, 950), true},
		{"колесо в сантиметрах вместо мм", wheel(base, 210), false},
		{"колесо от грузовика", wheel(base, 3000), false},

		{"сброс первого препятствия", obstacle(base, CmdDrop, 0), true},
		{"сброс последнего", obstacle(base, CmdDrop, MaxObstacles-1), true},
		{"взвести препятствие", obstacle(base, CmdArm, 3), true},
		{"препятствие вне диапазона", obstacle(base, CmdDrop, MaxObstacles), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.ok != (err == nil) {
				t.Fatalf("ожидалось ok=%v, получено %v", c.ok, err)
			}
		})
	}
}

func withKind(c Command, k CommandKind) Command { c.Kind = k; return c }

func limits(c Command, limit, restore float64) Command {
	c.Kind, c.LimitC, c.RestoreC = CmdSetLimits, limit, restore
	return c
}

func period(c Command, ms uint32) Command {
	c.Kind, c.PeriodMS = CmdSetPeriod, ms
	return c
}

func wheel(c Command, mm uint32) Command {
	c.Kind, c.WheelMM = CmdSetWheel, mm
	return c
}

func obstacle(c Command, kind CommandKind, index uint8) Command {
	c.Kind, c.Obstacle = kind, index
	return c
}

// Формат должен оставаться разбираемым линейным проходом на стороне платы.
func TestCommandEncode(t *testing.T) {
	cases := map[string]Command{
		"cmd=heater_off id=a1 seq=7": {ID: "a1", Kind: CmdHeaterOff, Seq: 7},
		"cmd=drop id=a4 seq=10 obstacle=2": {ID: "a4", Kind: CmdDrop, Seq: 10, Obstacle: 2},
		"cmd=set_wheel id=a5 seq=11 wheel_mm=2096": {
			ID: "a5", Kind: CmdSetWheel, Seq: 11, WheelMM: 2096},
		"cmd=set_limits id=a2 seq=8 limit_c=40.00 restore_c=37.00": {
			ID: "a2", Kind: CmdSetLimits, Seq: 8, LimitC: 40, RestoreC: 37},
		"cmd=set_period id=a3 seq=9 period_ms=200": {
			ID: "a3", Kind: CmdSetPeriod, Seq: 9, PeriodMS: 200},
	}

	for want, cmd := range cases {
		if got := cmd.Encode(); got != want {
			t.Errorf("получено %q, ожидалось %q", got, want)
		}
		if len(want) > MaxCommandLen {
			t.Errorf("команда не влезает в буфер платы: %d байт", len(want))
		}
	}
}

func TestParseAck(t *testing.T) {
	got, err := ParseAck("ack=a1 ok=0 state=off reason=overtemp uptime_ms=123456")
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if got.ID != "a1" || got.Accepted || got.Reason != "overtemp" || got.UptimeMS != 123456 {
		t.Errorf("подтверждение разобрано неверно: %+v", got)
	}

	if _, err := ParseAck("мусор"); !errors.Is(err, ErrBadCommand) {
		t.Errorf("мусор должен отвергаться, получено %v", err)
	}
	if !IsAck([]byte("ack=a1 ok=1")) || IsAck([]byte(`{"rpm":1}`)) {
		t.Error("подтверждение не отличается от телеметрии")
	}
}

// Отказ должен объяснять, что не так: текст попадает прямо в панель.
func TestОтказОбъясняетПричину(t *testing.T) {
	cases := map[string]Command{
		"возврата":  {ID: "a1", Device: "d", Kind: CmdSetLimits, LimitC: 40, RestoreC: 45},
		"диапазона": {ID: "a1", Device: "d", Kind: CmdSetLimits, LimitC: 200, RestoreC: 37},
		"колеса":    {ID: "a1", Device: "d", Kind: CmdSetWheel, WheelMM: 10},
		"препятствие": {ID: "a1", Device: "d", Kind: CmdDrop, Obstacle: 99},
	}

	for hint, cmd := range cases {
		err := cmd.Validate()
		if err == nil {
			t.Errorf("%s: команда должна быть отвергнута", hint)
			continue
		}
		if !strings.Contains(err.Error(), hint[:6]) {
			t.Errorf("причина отказа не объясняет проблему (%s): %v", hint, err)
		}
	}
}
