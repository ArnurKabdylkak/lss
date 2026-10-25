package app

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Оборудование помещения: то, чем сборщик управляет по командам
// центра, адресованным ему самому.
//
// Центр не знает ни выводов, ни полярности, ни числа прожекторов в
// зале: он говорит «подготовка», «включи дым», «авария». Соответствие
// узлов выводам живёт здесь — там же, где и провода.

// Room — обвязка помещения: узлы, завалы, замки и акустика.
type Room struct {
	cfg     RoomConfig
	outputs *Outputs
	log     Logger

	mu    sync.Mutex
	state map[string]bool // узел -> включён
	alarm bool
}

// Logger — то, что нужно помещению от журнала.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

func NewRoom(cfg RoomConfig, outputs *Outputs, log Logger) *Room {
	return &Room{cfg: cfg, outputs: outputs, log: log, state: map[string]bool{}}
}

// Prepare — команда подготовки помещения.
//
// Состояние узлов задаёт центр отдельными командами: какой узел
// включается при старте, а какой нет, зависит от сценария комнаты, и
// держать этот список ещё и здесь значило бы завести второй источник
// правды. Команда оставлена в контракте: по ней сборщик снимает
// запрет и готов принимать узлы.
func (r *Room) Prepare(on bool) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	alarm := r.alarm
	r.mu.Unlock()
	if alarm && on {
		// В аварии подготовку не начинают: пока она не снята,
		// включать нечего.
		return fmt.Errorf("помещение в аварии")
	}

	// Снятие подготовки обесточивает всё: это безопасное состояние,
	// а не сценарий — решать, что оставить включённым, центру.
	if !on {
		return r.allOff()
	}
	return nil
}

// allOff обесточивает все узлы помещения.
func (r *Room) allOff() error {
	var failed []string
	for _, u := range r.cfg.Units {
		if err := r.setUnit(u, false); err != nil {
			failed = append(failed, u.Unit)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("узлы не обесточены: %s", strings.Join(failed, ", "))
	}
	return nil
}

// SetUnit включает или выключает один узел: и ручное управление
// оператора, и сценарные команды центра приходят сюда.
func (r *Room) SetUnit(unit string, on bool) error {
	if r == nil {
		return fmt.Errorf("помещение не настроено")
	}

	// Авария состояние узлов не запрещает: сценарий аварии живёт в
	// центре — он сам обесточивает нагрузку и включает свет с
	// вытяжкой, присылая эти узлы следом за командой alarm. Нагрузку
	// в любом случае снимает аппаратная петля реле безопасности.
	for _, u := range r.cfg.Units {
		if u.Unit == unit {
			return r.setUnit(u, on)
		}
	}
	return fmt.Errorf("узла %q в помещении нет", unit)
}

// Alarm переводит помещение в безопасное состояние.
//
// Это дублирование аппаратной петли: нагрузку в любом случае снимает
// реле безопасности. Программный путь нужен, чтобы свет и вытяжка
// включились, а замки отпустились.
//
// Оповещения здесь нет: звук живёт в центре, у него и акустика, и
// записанные фразы. Сборщик о звуковых файлах не знает вовсе.
func (r *Room) Alarm() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	r.alarm = true
	r.mu.Unlock()

	// Обесточиваем всё: это примитив безопасного состояния, а не
	// сценарий. Что включить обратно — свет, вытяжку — решит центр
	// отдельными командами сразу следом.
	var failed []string
	if err := r.allOff(); err != nil {
		failed = append(failed, err.Error())
	}

	// Замки отпускаются всегда: из задымлённого зала должны выйти.
	for _, id := range r.cfg.Locks {
		if err := r.drive(id, false); err != nil {
			failed = append(failed, "замок "+id)
		}
	}

	// Магниты завалов не трогаем: аварийное открытие дверей не
	// должно ронять трубы, их безопасное состояние задаёт
	// конструкция механизма.

	if len(failed) > 0 {
		return fmt.Errorf("не переведено в безопасное состояние: %s",
			strings.Join(failed, ", "))
	}
	return nil
}

// AlarmReset разрешает работу после аварии.
//
// Оборудование само не возобновляется: команда лишь снимает запрет,
// а включает его администратор, проверив готовность зала.
func (r *Room) AlarmReset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.alarm = false
	r.mu.Unlock()
}

// Rubble отпускает трубы механизма «Завалы».
func (r *Room) Rubble() error {
	if r == nil || len(r.cfg.Rubble) == 0 {
		return fmt.Errorf("механизм «Завалы» не настроен")
	}

	r.mu.Lock()
	alarm := r.alarm
	r.mu.Unlock()
	if alarm {
		return fmt.Errorf("помещение в аварии")
	}

	var failed []string
	for _, id := range r.cfg.Rubble {
		if err := r.drive(id, true); err != nil {
			failed = append(failed, id)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("завалы не отпущены: %s", strings.Join(failed, ", "))
	}
	return nil
}

// State возвращает состояние узлов — для локальной панели сборщика.
func (r *Room) State() map[string]bool {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(map[string]bool, len(r.state))
	for k, v := range r.state {
		out[k] = v
	}
	return out
}

// InAlarm сообщает, что помещение в аварии.
func (r *Room) InAlarm() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alarm
}

func (r *Room) setUnit(u UnitConfig, on bool) error {
	var failed []string
	for _, id := range u.Outputs {
		if err := r.drive(id, on); err != nil {
			failed = append(failed, id)
		}
	}

	if len(failed) > 0 {
		r.log.Error("узел не переключился", "узел", u.Unit,
			"выходы", strings.Join(failed, ", "), "включить", on)
		return fmt.Errorf("выходы %s", strings.Join(failed, ", "))
	}

	r.mu.Lock()
	r.state[u.Unit] = on
	r.mu.Unlock()

	r.log.Info("узел помещения переключён", "узел", u.Unit, "включён", on)
	return nil
}

// drive щёлкает выходом по его имени из настройки.
func (r *Room) drive(id string, on bool) error {
	out, ok := r.outputs.ByID(id)
	if !ok {
		// Молчаливое согласие хуже отказа: оператор решил бы, что
		// команда исполнена, а реле не шевельнулось.
		return fmt.Errorf("выход %q не настроен", id)
	}

	st := out.Allow(on)
	if on && !st.On {
		// У нагрева защита могла не дать включить — это не сбой, но
		// и не исполнение.
		return fmt.Errorf("выход %q не включился: %s", id, st.Reason)
	}
	return nil
}

// uptimeSince — время работы сборщика, для подтверждений.
var started = time.Now()
