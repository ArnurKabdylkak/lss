package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

func record(t telemetry.Telemetry) telemetry.Record {
	return telemetry.Record{DeviceID: "stm32-01", Received: time.Now(), Data: t}
}

func sensor(rpm float64) telemetry.Record {
	return record(telemetry.Telemetry{RPM: rpm})
}

func relayEvent() telemetry.Record {
	return record(telemetry.Telemetry{Event: telemetry.EventRelay, State: "off", Reason: "overtemp"})
}

func openTemp(t *testing.T, max int) (*DiskBuffer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buffer.jsonl")

	b, err := OpenBuffer(path, max)
	if err != nil {
		t.Fatalf("открытие буфера: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b, path
}

// Ради этого буфер и заведён: перезапуск процесса не должен терять
// накопленное, пока канал наверх лежит.
func TestБуферПереживаетПерезапуск(t *testing.T) {
	b, path := openTemp(t, 100)

	for i := range 5 {
		if err := b.Append(sensor(float64(1500 + i))); err != nil {
			t.Fatalf("запись: %v", err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatalf("закрытие: %v", err)
	}

	again, err := OpenBuffer(path, 100)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer again.Close()

	if again.Len() != 5 {
		t.Fatalf("восстановлено %d записей из 5", again.Len())
	}
	if got := again.Peek(1)[0].Data.RPM; got != 1500 {
		t.Errorf("порядок записей нарушен: первая rpm=%v", got)
	}
}

// Записи удаляются только после подтверждения центром: иначе разрыв в
// момент выгрузки означал бы потерю данных.
func TestЗаписиУдаляютсяТолькоПослеПодтверждения(t *testing.T) {
	b, _ := openTemp(t, 100)

	b.Append(sensor(1))
	b.Append(sensor(2))

	peeked := b.Peek(2)
	if len(peeked) != 2 {
		t.Fatalf("прочитано %d записей", len(peeked))
	}
	if b.Len() != 2 {
		t.Error("чтение не должно удалять записи")
	}

	if err := b.Commit(2); err != nil {
		t.Fatalf("подтверждение: %v", err)
	}
	if b.Len() != 0 {
		t.Errorf("после подтверждения осталось %d записей", b.Len())
	}
}

// При переполнении вытесняются показания, но не события защиты.
func TestСобытияНеВытесняются(t *testing.T) {
	b, _ := openTemp(t, 3)

	b.Append(relayEvent())
	b.Append(sensor(1))
	b.Append(sensor(2))
	b.Append(sensor(3))
	b.Append(sensor(4))

	var events int
	for _, r := range b.Peek(b.Len()) {
		if r.Data.IsEvent() {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("событие вытеснено: событий в буфере %d", events)
	}
	if b.Len() > 3 {
		t.Errorf("буфер перерос предел: %d записей", b.Len())
	}
}

// Обрыв питания рвёт последнюю строку файла; это не повод терять всё.
func TestБитыйХвостНеЛомаетБуфер(t *testing.T) {
	b, path := openTemp(t, 100)
	b.Append(sensor(1500))
	b.Close()

	f, err := openAppend(path)
	if err != nil {
		t.Fatalf("дозапись: %v", err)
	}
	f.WriteString(`{"device_id":"stm32-01","rec`) // оборванная строка
	f.Close()

	again, err := OpenBuffer(path, 100)
	if err != nil {
		t.Fatalf("открытие после обрыва: %v", err)
	}
	defer again.Close()

	if again.Len() != 1 {
		t.Errorf("записей %d, ожидалась 1 (целая)", again.Len())
	}
}
