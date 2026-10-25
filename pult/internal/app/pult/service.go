// Package pult — работа пульта: опрос кнопок, отправка команд и
// зажигание ламп по подтверждённому состоянию.
//
// Логики сценария здесь нет. Пульт — это вторая панель оператора,
// сделанная из кнопок: что включается при старте и что остаётся
// включённым в аварии, решает центр.
package pult

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/pult/internal/domain/console"
)

// Center — то, что пульту нужно от центра.
type Center interface {
	State(ctx context.Context) (Snapshot, error)
	SetUnit(ctx context.Context, room, unit string, on bool) error
	Alarm(ctx context.Context, reason string) error
}

// Snapshot — состояние объекта, как его отдал центр.
type Snapshot struct {
	Units map[UnitKey]UnitState
	Alarm bool
}

// UnitKey — узел конкретного помещения.
type UnitKey struct {
	Room string
	Unit string
}

// UnitState — подтверждённое состояние узла и ожидание по нему.
type UnitState struct {
	On   bool
	Wait bool
}

// Fallback — запасной путь аварии, минуя центр.
type Fallback interface {
	Enabled() bool
	Alarm(ctx context.Context, reason string) error
}

// Board — железо пульта.
type Board interface {
	// ReadButtons читает оба порта: кнопки могут стоять на любом,
	// а на объекте порт B бывает занят ими целиком.
	ReadButtons(addr uint8) ([2]byte, error)
	// SetLamps зажигает lit из ног lamps. Погашенная лампа остаётся
	// высокоимпедансной: прижатый к земле выход служит обратным
	// путём для тока через светодиоды соседних кнопок.
	SetLamps(addr uint8, port console.Port, lamps, lit byte, sink bool) error
}

// Options — настройки опроса.
type Options struct {
	// Scan — период опроса кнопок. Двадцать миллисекунд — нажатие
	// человеком заведомо длиннее.
	Scan time.Duration
	// Debounce — сколько кнопка должна держать уровень, чтобы её
	// приняли. Механический контакт звенит единицы миллисекунд, но
	// на длинных линиях пульта звон затягивается, поэтому порог
	// взят с запасом: 30 мс человек не замечает, а ложное повторное
	// срабатывание на оборудовании зала заметит сразу.
	Debounce time.Duration
	// Poll — период чтения состояния из центра.
	Poll time.Duration
	// Blink — период мигания лампы узла, ждущего подтверждения.
	Blink time.Duration
	// AlarmReason — что записывается в журнал центра.
	AlarmReason string
}

func (o *Options) defaults() {
	if o.Scan <= 0 {
		// Опрос заметно чаще выдержки: антидребезг считает выборки,
		// и при двух выборках на выдержку одиночная наводка весит
		// половину решения. Шесть — уже запас.
		o.Scan = 5 * time.Millisecond
	}
	if o.Debounce <= 0 {
		o.Debounce = 30 * time.Millisecond
	}
	if o.Poll <= 0 {
		o.Poll = 500 * time.Millisecond
	}
	if o.Blink <= 0 {
		o.Blink = 400 * time.Millisecond
	}
	if o.AlarmReason == "" {
		o.AlarmReason = "аварийная кнопка на пульте"
	}
}

// Service — пульт целиком.
type Service struct {
	con    console.Console
	board  Board
	center Center
	back   Fallback
	opts   Options
	log    *slog.Logger

	// steps — сколько выборок подряд нужно, чтобы принять уровень.
	// Считается из выдержки и периода опроса: выдержка задаётся
	// временем, а интегратор работает в выборках.
	steps int

	mu    sync.RWMutex
	state Snapshot
	// asked — хотя бы один опрос центра уже сделан.
	asked bool

	// linked — отвечает ли центр. Нужно не ради статистики: когда
	// связи нет, обычные кнопки бессмысленны, а авария идёт в обход.
	linked bool

	// latched — грибок нажат и ещё не отпущен. Авария объявляется
	// один раз на нажатие: грибок с фиксацией держит контакт сколь
	// угодно долго, и без защёлки пульт слал бы её без остановки.
	latched bool
}

func New(con console.Console, board Board, c Center, back Fallback,
	opts Options, log *slog.Logger) *Service {

	opts.defaults()

	// Меньше двух выборок не бывает: с одной интегратор вырождается
	// в прямое чтение, и любая наводка проходит насквозь.
	steps := int(opts.Debounce / opts.Scan)
	if steps < 2 {
		steps = 2
	}

	return &Service{
		con: con, board: board, center: c, back: back,
		opts: opts, steps: steps, log: log,
		state: Snapshot{Units: map[UnitKey]UnitState{}},
	}
}

// Run ведёт пульт до отмены контекста.
func (s *Service) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() { defer wg.Done(); s.pollCenter(ctx) }()
	go func() { defer wg.Done(); s.scanBoard(ctx) }()

	wg.Wait()
	s.allLampsOff()
	return nil
}

// pollCenter держит у пульта свежее состояние объекта.
func (s *Service) pollCenter(ctx context.Context) {
	t := time.NewTicker(s.opts.Poll)
	defer t.Stop()

	for {
		snap, err := s.center.State(ctx)
		s.mu.Lock()
		if err != nil {
			// Первый отказ тоже пишется в журнал. Без этого пульт,
			// не достучавшийся до центра ни разу, молчал бы о
			// причине, а в зале это выглядит как мёртвые кнопки.
			if s.linked || !s.asked {
				s.log.Error("центр не отвечает: кнопки пульта не работают", "err", err)
			}
			s.linked = false
		} else {
			if !s.linked {
				s.log.Info("связь с центром восстановлена")
			}
			s.linked = true
			s.state = snap
		}
		s.asked = true
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// scanBoard опрашивает кнопки и обновляет лампы.
func (s *Service) scanBoard(ctx context.Context) {
	t := time.NewTicker(s.opts.Scan)
	defer t.Stop()

	buttons := s.buttonIndex()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		for _, addr := range s.con.Addrs() {
			raw, err := s.board.ReadButtons(addr)
			if err != nil {
				s.log.Error("расширитель не прочитан", "addr", addr, "err", err)
				continue
			}
			s.handlePort(ctx, addr, raw, buttons)
		}
		s.updateLamps()
	}
}

// button — кнопка вместе с тем, что она делает.
type button struct {
	room string
	unit string
	name string
	// emergency — это грибок, а не узел.
	emergency bool
	state     debounce
	// known — начальное положение уже усвоено. До этого кнопка не
	// «нажимается»: при старте часть кнопок может быть нажата, и
	// принимать это за нажатие нельзя — пульт разом отправил бы
	// команды по всем узлам.
	known bool
}

// debounce гасит дребезг и наводки интегрированием.
//
// Счётчик растёт, пока вход читается нажатым, и падает, пока
// отпущенным; состояние меняется только у края. Это надёжнее, чем
// «уровень держится N миллисекунд»: одиночный выброс посреди
// выдержки такую проверку обнуляет и начинает отсчёт заново, а здесь
// он лишь отнимает один шаг из нескольких. На длинных линиях пульта,
// где помеха приходит пачками, разница решающая.
type debounce struct {
	count int  // 0..limit
	level bool // принятое состояние
}

// prime задаёт исходное состояние по первой выборке.
//
// Без этого счётчик, стоящий на нуле, неотличим от отпущенной
// кнопки, и первое же настоящее нажатие службе пришлось бы принять
// за начальное положение.
func (d *debounce) prime(sample bool, limit int) {
	d.level = sample
	if sample {
		d.count = limit
	} else {
		d.count = 0
	}
}

// step принимает очередную выборку и сообщает, изменилось ли
// принятое состояние.
func (d *debounce) step(sample bool, limit int) (changed bool) {
	if sample {
		d.count++
	} else {
		d.count--
	}
	switch {
	case d.count >= limit:
		d.count = limit
		if !d.level {
			d.level = true
			return true
		}
	case d.count <= 0:
		d.count = 0
		if d.level {
			d.level = false
			return true
		}
	}
	return false
}

// buttonIndex раскладывает кнопки по адресу микросхемы и ноге.
func (s *Service) buttonIndex() map[uint8]map[console.Pin]*button {
	out := map[uint8]map[console.Pin]*button{}
	add := func(addr uint8, pin console.Pin, b *button) {
		if out[addr] == nil {
			out[addr] = map[console.Pin]*button{}
		}
		out[addr][pin] = b
	}

	for _, r := range s.con.Rooms {
		for _, u := range r.Units {
			add(r.Addr, u.Button, &button{room: r.ID, unit: u.Unit, name: u.Name})
		}
	}
	e := s.con.Emergency
	add(e.Addr, e.Button, &button{name: "аварийная кнопка", emergency: true})
	return out
}

// handlePort разбирает прочитанный байт кнопок одной микросхемы.
func (s *Service) handlePort(ctx context.Context, addr uint8, raw [2]byte,
	index map[uint8]map[console.Pin]*button) {

	for pin, b := range index[addr] {
		level := raw[pin.Port]&pin.Mask() != 0

		// Грибок нормально замкнутый: в покое цепь целая. Инверсия
		// входа на микросхеме уже сделала «замкнуто» единицей,
		// поэтому нажатие — это ноль. Обрыв провода даёт то же самое
		// и трактуется как нажатие: отказ в безопасную сторону.
		sample := level
		if b.emergency && s.con.Emergency.NormallyClosed {
			sample = !level
		}

		// Начальное положение запоминаем молча: при старте часть
		// кнопок может оказаться нажатой, и принимать это за
		// нажатие нельзя.
		if !b.known {
			b.known = true
			b.state.prime(sample, s.steps)
			if sample {
				s.log.Warn("при старте кнопка уже нажата, считаю это положением покоя",
					"кнопка", b.name, "помещение", b.room)
			}
			continue
		}

		if !b.state.step(sample, s.steps) {
			continue
		}
		pressed := b.state.level

		if b.emergency {
			s.emergency(ctx, pressed)
			continue
		}
		// Узлы переключаются по нажатию, отпускание ничего не делает.
		if pressed {
			s.press(ctx, b)
		}
	}
}

// emergency обрабатывает грибок.
func (s *Service) emergency(ctx context.Context, pressed bool) {
	s.mu.Lock()
	was := s.latched
	s.latched = pressed
	linked := s.linked
	s.mu.Unlock()

	if !pressed {
		if was {
			s.log.Info("аварийная кнопка отпущена")
		}
		return
	}
	if was {
		return
	}

	s.log.Error("АВАРИЙНАЯ КНОПКА НАЖАТА НА ПУЛЬТЕ")

	// Сначала центр: он остановит занятия, запишет журнал и разошлёт
	// сценарий аварии по помещениям.
	err := s.center.Alarm(ctx, s.opts.AlarmReason)
	if err == nil {
		return
	}
	s.log.Error("центр не принял аварию", "err", err, "связь", linked)

	// Центра нет — останавливаем помещения напрямую. Оборудование
	// обесточится, а центр узнает об аварии, когда вернётся: пульт
	// продолжит слать ему вызов, пока он не ответит.
	if s.back.Enabled() {
		if err := s.back.Alarm(ctx, s.opts.AlarmReason); err != nil {
			s.log.Error("прямая остановка помещений не удалась", "err", err)
		}
	}
	go s.retryCenterAlarm(ctx)
}

// retryCenterAlarm добивается, чтобы авария попала в центр.
//
// Остановка уже выполнена напрямую, но в центре авария должна быть
// видна и записана в журнал: по нему разбирают происшествие.
func (s *Service) retryCenterAlarm(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		s.mu.RLock()
		latched := s.latched
		s.mu.RUnlock()
		if !latched {
			// Грибок отпустили, не дождавшись центра: аварию снимает
			// оператор в панели, навязывать её задним числом нельзя.
			return
		}

		if err := s.center.Alarm(ctx, s.opts.AlarmReason); err == nil {
			s.log.Info("авария доведена до центра")
			return
		}
	}
}

// press переключает узел: лампа покажет результат, когда сборщик
// подтвердит.
func (s *Service) press(ctx context.Context, b *button) {
	s.mu.RLock()
	linked := s.linked
	st := s.state.Units[UnitKey{Room: b.room, Unit: b.unit}]
	alarm := s.state.Alarm
	s.mu.RUnlock()

	if !linked {
		s.log.Warn("кнопка нажата без связи с центром, команда не отправлена",
			"помещение", b.room, "узел", b.unit)
		return
	}
	if alarm {
		s.log.Warn("кнопка нажата в аварии, команда не отправлена",
			"помещение", b.room, "узел", b.unit)
		return
	}
	if st.Wait {
		// Предыдущая команда ещё не подтверждена: второе нажатие
		// отправило бы обратное и запутало бы оператора.
		return
	}

	next := !st.On
	s.log.Info("кнопка пульта", "помещение", b.room, "узел", b.unit, "включить", next)
	if err := s.center.SetUnit(ctx, b.room, b.unit, next); err != nil {
		s.log.Error("команда с пульта не ушла",
			"помещение", b.room, "узел", b.unit, "err", err)
	}
}

// updateLamps зажигает лампы по подтверждённому состоянию.
func (s *Service) updateLamps() {
	s.mu.RLock()
	state := s.state
	linked := s.linked
	s.mu.RUnlock()

	// Мигание отсчитывается от часов, а не от числа проходов: период
	// опроса может плавать, а мигать лампы должны ровно.
	phase := time.Now().UnixMilli()/int64(s.opts.Blink/time.Millisecond)%2 == 0

	for _, r := range s.con.Rooms {
		var portA, portB byte
		for _, u := range r.Units {
			if !u.HasLamp {
				continue
			}
			st := state.Units[UnitKey{Room: r.ID, Unit: u.Unit}]

			// Нет связи — гасим всё: показывать последнее известное
			// состояние опаснее, чем не показывать ничего.
			lit := linked && st.On
			if linked && st.Wait {
				lit = phase
			}
			// На смонтированном пульте лампа зажигается нулём на ноге:
			// пишем инверсию, чтобы горели включённые узлы.
			if lit {
				continue
			}
			if u.Lamp.Port == console.PortA {
				portA |= u.Lamp.Mask()
			} else {
				portB |= u.Lamp.Mask()
			}
		}

		if r.Addr == s.con.Emergency.Addr && state.Alarm && s.con.Emergency.HasLamp {
			if s.con.Emergency.Lamp.Port == console.PortA {
				portA |= s.con.Emergency.Lamp.Mask()
			} else {
				portB |= s.con.Emergency.Lamp.Mask()
			}
		}

		maskA, maskB := s.lampMasks(r.Addr)
		if maskB != 0 {
			s.write(r.Addr, console.PortB, maskB, portB)
		}
		// Порт A занят кнопками: лампы на нём допустимы только на
		// свободных ногах, и трогаем мы ровно их биты.
		if maskA != 0 {
			s.write(r.Addr, console.PortA, maskA, portA)
		}
	}
}

func (s *Service) write(addr uint8, port console.Port, lamps, lit byte) {
	if err := s.board.SetLamps(addr, port, lamps, lit, s.con.Sink); err != nil {
		s.log.Error("лампы не обновлены", "addr", addr, "err", err)
	}
}

// lampMasks — какие ноги микросхемы заняты лампами.
func (s *Service) lampMasks(addr uint8) (portA, portB byte) {
	_, lamps := s.con.Masks(addr)
	return lamps[console.PortA], lamps[console.PortB]
}

// allLampsOff гасит пульт при остановке службы: горящая лампа у
// выключенного пульта врёт о состоянии зала.
func (s *Service) allLampsOff() {
	for _, addr := range s.con.Addrs() {
		maskA, maskB := s.lampMasks(addr)
		// Погашенная лампа — единица на ноге (см. updateLamps).
		s.write(addr, console.PortA, maskA, maskA)
		s.write(addr, console.PortB, maskB, maskB)
	}
}
