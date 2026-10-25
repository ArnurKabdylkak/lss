package queue

import (
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

func cmd(id string) telemetry.Command {
	return telemetry.Command{ID: id, Device: "stm32-01", Kind: telemetry.CmdHeaterOff}
}

// Каждое обращение сборщика не должно плодить дубли команды в эфире.
func TestКомандаНеВыдаётсяПовторноСразу(t *testing.T) {
	q := NewMemory()
	q.Enqueue(t.Context(), "obj-17", cmd("a1"))

	first, _ := q.Take(t.Context(), "obj-17", 10, nil)
	second, _ := q.Take(t.Context(), "obj-17", 10, nil)

	if len(first) != 1 {
		t.Fatalf("первая выдача вернула %d команд", len(first))
	}
	if len(second) != 0 {
		t.Errorf("команда выдана повторно до истечения паузы")
	}
}

// Если подтверждения нет дольше паузы, команду надо отправить снова:
// UDP не гарантирует доставку.
func TestКомандаПовторяетсяБезПодтверждения(t *testing.T) {
	q := NewMemory()
	q.Retry = time.Millisecond
	q.Enqueue(t.Context(), "obj-17", cmd("a1"))

	q.Take(t.Context(), "obj-17", 10, nil)
	time.Sleep(2 * time.Millisecond)

	again, _ := q.Take(t.Context(), "obj-17", 10, nil)
	if len(again) != 1 {
		t.Fatal("команда без подтверждения не переотправлена")
	}

	pending, _ := q.Pending(t.Context(), "obj-17")
	if pending[0].Attempts != 2 {
		t.Errorf("попыток %d, ожидалось 2", pending[0].Attempts)
	}
}

func TestПодтверждённаяКомандаБольшеНеВыдаётся(t *testing.T) {
	q := NewMemory()
	q.Retry = time.Millisecond
	q.Enqueue(t.Context(), "obj-17", cmd("a1"))
	q.Take(t.Context(), "obj-17", 10, nil)

	settled, known, _ := q.Settle(t.Context(),
		telemetry.Ack{ID: "a1", Accepted: true, State: "off"})
	if !known || settled.ID != "a1" {
		t.Errorf("подтверждение не вернуло команду: %+v, известна=%v", settled, known)
	}
	time.Sleep(2 * time.Millisecond)

	if again, _ := q.Take(t.Context(), "obj-17", 10, nil); len(again) != 0 {
		t.Error("подтверждённая команда выдана снова")
	}
}

// Подтверждение команды, выданной до перезапуска центра, — не ошибка.
func TestПодтверждениеНеизвестнойКомандыНеОшибка(t *testing.T) {
	q := NewMemory()
	_, known, err := q.Settle(t.Context(), telemetry.Ack{ID: "нет такой"})
	if err != nil {
		t.Errorf("неожиданная ошибка: %v", err)
	}
	if known {
		t.Error("неизвестная команда названа известной")
	}
}

func TestОчередиОбъектовНеПересекаются(t *testing.T) {
	q := NewMemory()
	q.Enqueue(t.Context(), "obj-17", cmd("a1"))
	q.Enqueue(t.Context(), "obj-18", cmd("a2"))

	got, _ := q.Take(t.Context(), "obj-17", 10, nil)
	if len(got) != 1 || got[0].ID != "a1" {
		t.Errorf("объект получил чужие команды: %+v", got)
	}
}
