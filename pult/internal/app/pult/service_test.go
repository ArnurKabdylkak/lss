package pult

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/pult/internal/domain/console"
)

// fakeBoard — пульт без железа: кнопки задаются из теста, лампы
// запоминаются.
type fakeBoard struct {
	mu    sync.Mutex
	in    map[uint8]byte
	lamps map[uint8]byte
}

func newBoard() *fakeBoard {
	return &fakeBoard{in: map[uint8]byte{}, lamps: map[uint8]byte{}}
}

func (b *fakeBoard) ReadButtons(addr uint8) ([2]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Тестовый пульт держит кнопки на порту A, порт B пуст.
	return [2]byte{b.in[addr], 0}, nil
}

func (b *fakeBoard) SetLamps(addr uint8, port console.Port, lamps, lit byte, _ bool) error {
	if port != console.PortB {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Трогаются только ноги ламп: на остальных кнопки.
	// Лампа горит нулём на ноге: в модели храним то, что видит человек.
	b.lamps[addr] = (b.lamps[addr] &^ lamps) | (lamps &^ lit)
	return nil
}

func (b *fakeBoard) press(addr uint8, mask byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.in[addr] |= mask
}

func (b *fakeBoard) release(addr uint8, mask byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.in[addr] &^= mask
}

func (b *fakeBoard) lamp(addr uint8, mask byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lamps[addr]&mask != 0
}

// fakeCenter — центр, которым управляет тест.
type fakeCenter struct {
	mu    sync.Mutex
	snap  Snapshot
	calls []string
	down  bool
	alarm int
}

func (c *fakeCenter) State(context.Context) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.down {
		return Snapshot{}, fmt.Errorf("центр недоступен")
	}
	// Копия, как у настоящего клиента: тот строит карту заново на
	// каждый ответ, и делить её с сервисом нельзя.
	out := Snapshot{Alarm: c.snap.Alarm, Units: make(map[UnitKey]UnitState, len(c.snap.Units))}
	for k, v := range c.snap.Units {
		out.Units[k] = v
	}
	return out, nil
}

func (c *fakeCenter) SetUnit(_ context.Context, room, unit string, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.down {
		return fmt.Errorf("центр недоступен")
	}
	c.calls = append(c.calls, fmt.Sprintf("%s/%s=%v", room, unit, on))
	return nil
}

func (c *fakeCenter) Alarm(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.down {
		return fmt.Errorf("центр недоступен")
	}
	c.alarm++
	return nil
}

func (c *fakeCenter) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func (c *fakeCenter) setUnitState(room, unit string, st UnitState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap.Units == nil {
		c.snap.Units = map[UnitKey]UnitState{}
	}
	c.snap.Units[UnitKey{Room: room, Unit: unit}] = st
}

type fakeBack struct {
	mu   sync.Mutex
	hits int
}

func (f *fakeBack) Enabled() bool { return true }
func (f *fakeBack) Alarm(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits++
	return nil
}
func (f *fakeBack) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func testConsole() console.Console {
	return console.Console{
		Rooms: []console.Room{{
			ID:   "room-1",
			Addr: 0x20,
			Units: []console.Unit{
				{Unit: "light", Name: "Освещение",
					Button:  console.Pin{Port: console.PortA, Bit: 0},
					Lamp:    console.Pin{Port: console.PortB, Bit: 0},
					HasLamp: true},
			},
		}},
		Emergency: console.Emergency{
			Addr:    0x20,
			Button:  console.Pin{Port: console.PortA, Bit: 7},
			Lamp:    console.Pin{Port: console.PortB, Bit: 7},
			HasLamp: true,
			// Для теста удобнее нормально разомкнутая: нажатие —
			// появление единицы. Поведение NC проверяется отдельно.
			NormallyClosed: false,
		},
	}
}

func start(t *testing.T, board Board, c Center, back Fallback) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(testConsole(), board, c, back, Options{
		Scan:     time.Millisecond,
		Debounce: 6 * time.Millisecond, // шесть выборок на решение
		Poll:     2 * time.Millisecond,
	}, log)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	// Пульт запоминает положение кнопок на старте и не принимает его
	// за нажатие. Тесту нужно дать ему это сделать, иначе первое же
	// нажатие окажется «тем самым начальным положением».
	time.Sleep(10 * time.Millisecond)
	return svc
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

// Нажатие кнопки отправляет в центр команду, противоположную тому,
// что сейчас подтверждено.
func TestНажатиеПереключаетУзел(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	c.setUnitState("room-1", "light", UnitState{On: false})
	start(t, board, c, &fakeBack{})

	board.press(0x20, 1<<0)

	eventually(t, "команда ушла в центр", func() bool {
		s := c.sent()
		return len(s) == 1 && s[0] == "room-1/light=true"
	})
}

// Лампа загорается только по подтверждённому состоянию, а не по
// нажатию: иначе оператор видит включённым то, чего не произошло.
func TestЛампаГоритТолькоПоПодтверждению(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	c.setUnitState("room-1", "light", UnitState{On: false, Wait: true})
	start(t, board, c, &fakeBack{})

	// Ожидание — лампа мигает, значит бывает погашенной.
	off := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !off {
		if !board.lamp(0x20, 1<<0) {
			off = true
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !off {
		t.Error("узел ждёт подтверждения, а лампа горит ровно")
	}

	c.setUnitState("room-1", "light", UnitState{On: true})
	eventually(t, "лампа загорелась после подтверждения", func() bool {
		return board.lamp(0x20, 1<<0)
	})
}

// Без связи с центром обычные кнопки молчат: команда всё равно
// исполняется центром, и копить нажатия опасно.
func TestБезСвязиКнопкиНеОтправляются(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	c.down = true
	start(t, board, c, &fakeBack{})

	board.press(0x20, 1<<0)
	time.Sleep(50 * time.Millisecond)

	if got := c.sent(); len(got) != 0 {
		t.Errorf("без связи ушли команды: %v", got)
	}
}

// Авария при недоступном центре уходит прямо в помещения.
func TestАварияБезЦентраИдётНапрямую(t *testing.T) {
	board, c, back := newBoard(), &fakeCenter{}, &fakeBack{}
	c.down = true
	start(t, board, c, back)

	board.press(0x20, 1<<7)
	eventually(t, "авария ушла напрямую", func() bool { return back.count() == 1 })
}

// Грибок с фиксацией держит контакт, но авария объявляется один раз.
func TestФиксированныйГрибокНеПовторяетАварию(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	start(t, board, c, &fakeBack{})

	board.press(0x20, 1<<7)
	eventually(t, "авария объявлена", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.alarm == 1
	})

	time.Sleep(50 * time.Millisecond)
	c.mu.Lock()
	n := c.alarm
	c.mu.Unlock()
	if n != 1 {
		t.Errorf("авария объявлена %d раз вместо одного", n)
	}

	// После отпускания грибок снова взводится.
	board.release(0x20, 1<<7)
	time.Sleep(20 * time.Millisecond)
	board.press(0x20, 1<<7)
	eventually(t, "повторная авария", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.alarm == 2
	})
}

// Дребезг контакта не превращается в несколько нажатий.
func TestДребезгНеДаётЛишнихКоманд(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	c.setUnitState("room-1", "light", UnitState{On: false})
	start(t, board, c, &fakeBack{})
	time.Sleep(10 * time.Millisecond)

	// Звон: уровень скачет быстрее времени устаканивания.
	for range 5 {
		board.press(0x20, 1<<0)
		time.Sleep(300 * time.Microsecond)
		board.release(0x20, 1<<0)
		time.Sleep(300 * time.Microsecond)
	}
	board.press(0x20, 1<<0)

	eventually(t, "одна команда", func() bool { return len(c.sent()) >= 1 })
	time.Sleep(50 * time.Millisecond)
	if got := c.sent(); len(got) != 1 {
		t.Errorf("дребезг дал %d команд: %v", len(got), got)
	}
}

// Кнопка, нажатая в момент старта службы, командой не становится.
//
// Так выглядит и неверно понятая полярность монтажа: все кнопки
// читаются нажатыми, и без этой защиты пульт при каждом запуске
// переключал бы разом всё оборудование всех помещений.
func TestНачальноеПоложениеНеСчитаетсяНажатием(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	c.setUnitState("room-1", "light", UnitState{On: false})
	board.press(0x20, 1<<0) // кнопка «нажата» ещё до запуска

	start(t, board, c, &fakeBack{})
	time.Sleep(50 * time.Millisecond)

	if got := c.sent(); len(got) != 0 {
		t.Errorf("начальное положение дало команды: %v", got)
	}

	// А вот отпускание и новое нажатие — уже настоящее.
	board.release(0x20, 1<<0)
	time.Sleep(20 * time.Millisecond)
	board.press(0x20, 1<<0)

	eventually(t, "нажатие после старта", func() bool { return len(c.sent()) == 1 })
}

// Одиночные выбросы на линии не проходят в команды.
//
// Так выглядит наводка на длинной линии пульта: уровень срывается на
// одну-две выборки и возвращается. Выдержка «держится N миллисекунд»
// на таком сбрасывала бы отсчёт и теряла настоящее нажатие;
// интегратор теряет один шаг и продолжает.
func TestНаводкаНеСтановитсяНажатием(t *testing.T) {
	board, c := newBoard(), &fakeCenter{}
	c.setUnitState("room-1", "light", UnitState{On: false})
	start(t, board, c, &fakeBack{})

	// Полсотни одиночных выбросов короче выдержки.
	for range 50 {
		board.press(0x20, 1<<0)
		time.Sleep(900 * time.Microsecond)
		board.release(0x20, 1<<0)
		time.Sleep(3 * time.Millisecond)
	}

	if got := c.sent(); len(got) != 0 {
		t.Errorf("наводка дала команды: %v", got)
	}

	// Настоящее нажатие после этого проходит.
	board.press(0x20, 1<<0)
	eventually(t, "нажатие после наводок", func() bool { return len(c.sent()) == 1 })
}
