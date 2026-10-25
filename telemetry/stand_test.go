package telemetry_test

import (
	"strings"
	"testing"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Контракт стойки набора чисел.
//
// Проверяется не модель, а провод: строки ниже — ровно те, что
// собирает прошивка (firmware, src/main.c и src/command.c), и
// команды в том виде, в каком их принимает её разборщик.
//
// Смысл теста в том, чтобы расхождение ловилось здесь, а не в зале.
// Стойка стоит в задымлённом помещении, её не видно и не слышно:
// если центр перестанет понимать её ответы, это обнаружится на
// занятии, когда звено упрётся в задание, которое не засчитывается.

// Датаграммы прошивки: событие клавиши, результат проверки кода и
// очередной отчёт о себе.
const (
	keyDatagram   = `{"event":"key","key":"7","uptime_ms":123456}`
	codeDatagram  = `{"event":"code","code_result":"valid","uptime_ms":123456}`
	aliveDatagram = `{"uptime_ms":123456}`
)

func TestСтойкаСобытиеКлавиши(t *testing.T) {
	got, err := telemetry.DecodeDatagram([]byte(keyDatagram))
	if err != nil {
		t.Fatalf("датаграмма не разобрана: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("датаграмма не прошла проверку: %v", err)
	}

	if !got.IsKeyEvent() {
		t.Errorf("событие разобрано как %q", got.EventName())
	}
	if got.Key != "7" {
		t.Errorf("клавиша: %q", got.Key)
	}
	if got.UptimeMS != 123456 {
		t.Errorf("время работы: %d", got.UptimeMS)
	}
}

func TestСтойкаРезультатКода(t *testing.T) {
	got, err := telemetry.DecodeDatagram([]byte(codeDatagram))
	if err != nil {
		t.Fatalf("датаграмма не разобрана: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("датаграмма не прошла проверку: %v", err)
	}

	if !got.IsCodeEvent() {
		t.Errorf("событие разобрано как %q", got.EventName())
	}
	// Именно это значение засчитывает задание в центре. Переименовать
	// его на одной стороне — значит разорвать зачёт.
	if got.CodeResult != "valid" {
		t.Errorf("результат: %q", got.CodeResult)
	}
}

// Прошивка сообщает и о неудачах: по ним считаются попытки, а пауза
// после подбора видна оператору.
func TestСтойкаВсеРезультатыРазбираются(t *testing.T) {
	for _, result := range []string{"valid", "invalid", "locked", "cleared"} {
		raw := `{"event":"code","code_result":"` + result + `","uptime_ms":1}`

		got, err := telemetry.DecodeDatagram([]byte(raw))
		if err != nil {
			t.Fatalf("%s: датаграмма не разобрана: %v", result, err)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("%s: не прошло проверку: %v", result, err)
		}
		if got.CodeResult != result {
			t.Errorf("%s: разобрано как %q", result, got.CodeResult)
		}
	}
}

// Отчёт о себе без показаний: измерять стойке нечего, но молчать
// нельзя — замолчавшее устройство сборщик считает пропавшим.
func TestСтойкаОтчётОСебе(t *testing.T) {
	got, err := telemetry.DecodeDatagram([]byte(aliveDatagram))
	if err != nil {
		t.Fatalf("датаграмма не разобрана: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("датаграмма не прошла проверку: %v", err)
	}
	if !got.IsSensorReading() {
		t.Errorf("отчёт о себе разобран как событие %q", got.EventName())
	}
	if got.UptimeMS != 123456 {
		t.Errorf("время работы: %d", got.UptimeMS)
	}
}

// Команда смены кода должна собираться в строку, которую понимает
// разборщик прошивки: пары ключ=значение через пробел, без JSON.
func TestСтойкаКомандаСменыКода(t *testing.T) {
	cmd := telemetry.Command{
		ID: "a1b2c5", Device: "obj-17-code-1",
		Kind: telemetry.CmdSetCode, Code: "4731", Seq: 9,
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("команда не прошла проверку: %v", err)
	}

	raw := cmd.Encode()
	if raw != "cmd=set_code id=a1b2c5 seq=9 code=4731" {
		t.Fatalf("команда на проводе: %q", raw)
	}
	if len(raw) > telemetry.MaxCommandLen {
		t.Errorf("команда длиннее буфера платы: %d > %d",
			len(raw), telemetry.MaxCommandLen)
	}

	// Поля идут парами «ключ=значение»: разборщик платы проходит
	// строку линейно и ничего, кроме пробелов, не ждёт.
	for field := range strings.FieldsSeq(raw) {
		if !strings.Contains(field, "=") {
			t.Errorf("поле без значения: %q", field)
		}
	}
}

// Код проверяется до отправки: плата отвергнет негодный, но тогда
// оператор узнает об этом из отказа, а не из панели.
func TestСтойкаНегодныйКодНеУходит(t *testing.T) {
	cases := map[string]string{
		"короче трёх цифр": "12",
		"длиннее предела":  "1234567890123",
		"не цифры":         "12a4",
	}

	for name, code := range cases {
		cmd := telemetry.Command{
			ID: "a1", Device: "obj-17-code-1",
			Kind: telemetry.CmdSetCode, Code: code,
		}
		if err := cmd.Validate(); err == nil {
			t.Errorf("%s: код %q принят", name, code)
		}
	}
}

// Подтверждение, которое шлёт плата в ответ на команду.
func TestСтойкаПодтверждение(t *testing.T) {
	ack, err := telemetry.ParseAck("ack=a1b2c5 ok=1 state=ok reason=ok uptime_ms=123456")
	if err != nil {
		t.Fatalf("подтверждение не разобрано: %v", err)
	}

	if ack.ID != "a1b2c5" || !ack.Accepted {
		t.Errorf("подтверждение: %+v", ack)
	}
	if ack.UptimeMS != 123456 {
		t.Errorf("время работы: %d", ack.UptimeMS)
	}

	// Отказ — осознанное решение платы, а не потеря команды: причина
	// должна доходить до журнала дословно.
	refused, err := telemetry.ParseAck(
		"ack=a1b2c6 ok=0 state=ok reason=bad_code uptime_ms=10")
	if err != nil {
		t.Fatalf("отказ не разобран: %v", err)
	}
	if refused.Accepted || refused.Reason != "bad_code" {
		t.Errorf("отказ: %+v", refused)
	}
}
