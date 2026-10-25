package app

import (
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Выходы сборщика: реле на выводах Raspberry Pi.
//
// Их несколько, и по смыслу они разные. Тепловой пушке нужна защита от
// перегрева, стробоскопу она не нужна и прямо мешает: без достоверной
// температуры защита не разрешает включение, а греть стробоскопу
// нечего. Поэтому тип выхода задаётся в конфигурации, а не
// предполагается одинаковым для всех.
//
// Команды из центра адресуются полем Device: отдельных видов команд на
// каждое реле не вводится, иначе общий контракт пришлось бы менять при
// каждом новом выходе на объекте.

// OutputKind — что висит на выводе.
type OutputKind string

const (
	OutputHeater OutputKind = "heater" // нагрев, с защитой от перегрева
	OutputSwitch OutputKind = "switch" // ключ без температурной логики
)

// Output — выход, которым командует центр.
type Output interface {
	ID() string
	Kind() OutputKind

	// Source — чип и линия в виде /dev/gpiochip0:17. Уходит в
	// телеметрию: по журналу центра должно быть видно, какой именно
	// вывод щёлкнул.
	Source() string

	// Allow разрешает или запрещает работу. Для пушки это именно
	// разрешение: включит ли реле, решает защита.
	Allow(allowed bool) HeaterState

	State() HeaterState
	Update() HeaterState

	// SetLimits осмысленна только для нагрева; ключ возвращает ошибку,
	// а не делает вид, что уставки приняты.
	SetLimits(limitC, restoreC float64) error

	Close() error
}

// Relay — выход без температурной логики: стробоскоп, дымогенератор,
// освещение. Включается командой и остаётся включённым.
type Relay struct {
	mu sync.Mutex

	id     string
	source string
	sw     Switch
	line   *GPIOLine
	log    *slog.Logger
	on     bool
	since  time.Time
}

// NewRelay создаёт ключ в безопасном состоянии — выключенным.
func NewRelay(id, source string, sw Switch, line *GPIOLine, log *slog.Logger) *Relay {
	r := &Relay{id: id, source: source, sw: sw, line: line, log: log, since: time.Now()}
	if sw != nil {
		if err := sw.Set(false); err != nil {
			log.Error("не удалось снять реле при запуске", "output", id, "err", err)
		}
	}
	return r
}

func (r *Relay) ID() string       { return r.id }
func (r *Relay) Kind() OutputKind { return OutputSwitch }
func (r *Relay) Source() string   { return r.source }

func (r *Relay) Allow(allowed bool) HeaterState {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.on != allowed {
		r.on = allowed
		r.since = time.Now()
		if r.sw != nil {
			if err := r.sw.Set(allowed); err != nil {
				r.log.Error("реле не переключилось", "output", r.id,
					"on", allowed, "err", err)
			}
		}
		r.log.Info("реле", "output", r.id, "on", r.on)
	}
	return r.stateLocked()
}

// Update у ключа ничего не пересчитывает: состояние меняется только
// командой. Метод есть ради единообразия с нагревом, который
// опрашивается по таймеру.
func (r *Relay) Update() HeaterState { return r.State() }

func (r *Relay) State() HeaterState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stateLocked()
}

func (r *Relay) stateLocked() HeaterState {
	reason := HeaterOperatorOff
	if r.on {
		reason = HeaterOK
	}
	return HeaterState{
		ID:      r.id,
		Kind:    OutputSwitch,
		On:      r.on,
		Allowed: r.on,
		Reason:  reason,
		Since:   r.since,
		Dry:     r.sw == nil,
	}
}

// SetLimits для ключа — ошибка конфигурации или оператора, и сказать об
// этом надо прямо: молчаливое согласие создало бы у оператора
// впечатление, что у стробоскопа есть тепловая защита.
func (r *Relay) SetLimits(float64, float64) error {
	return fmt.Errorf("выход %s не тепловой: уставки защиты к нему не применяются", r.id)
}

func (r *Relay) Close() error {
	// Безопасное состояние при остановке: то же, что у нагрева.
	r.Allow(false)
	if r.line != nil {
		return r.line.Close()
	}
	return nil
}

// Outputs — все выходы объекта, с поиском по идентификатору.
//
// Коллекция знает ещё и то, какой нагрев слушает какие датчики:
// рассылать температуру всем подряд нельзя, пушки стоят в разных точках
// и защита у каждой своя.
type Outputs struct {
	list []Output
	byID map[string]Output

	heaters []heaterSource
	localID string // идентификатор своего датчика сборщика
}

type heaterSource struct {
	h *Heater
	// Пустой список означает «все устройства объекта»: обычно рядом с
	// пушкой стоит одна плата, но требовать её перечисления незачем.
	from []string
}

// ByID находит выход, которому адресована команда.
func (o *Outputs) ByID(id string) (Output, bool) {
	if o == nil {
		return nil, false
	}
	out, ok := o.byID[id]
	return out, ok
}

func (o *Outputs) All() []Output {
	if o == nil {
		return nil
	}
	return o.list
}

func (o *Outputs) Len() int {
	if o == nil {
		return 0
	}
	return len(o.list)
}

// ObserveTemp раздаёт показание тем выходам, для которых оно
// предназначено. Ключи его игнорируют: температура к ним не относится.
func (o *Outputs) ObserveTemp(deviceID string, tempC float64, at time.Time) {
	if o == nil {
		return
	}
	for _, src := range o.heaters {
		if !sourceAllowed(src.from, deviceID) {
			continue
		}
		// Свой датчик и датчик платы учитываются по-разному: второй идёт
		// по сети, и устаревшие показания защита не принимает.
		if deviceID == o.localID {
			src.h.ObserveLocal(tempC, at)
		} else {
			src.h.ObserveBoard(tempC, at)
		}
	}
}

func sourceAllowed(from []string, deviceID string) bool {
	if len(from) == 0 {
		return true
	}
	return slices.Contains(from, deviceID)
}

// UpdateAll пересчитывает состояние всех выходов и возвращает те, что
// изменились с прошлого раза, — их нужно опубликовать.
func (o *Outputs) UpdateAll() []HeaterState {
	if o == nil {
		return nil
	}
	states := make([]HeaterState, 0, len(o.list))
	for _, out := range o.list {
		states = append(states, out.Update())
	}
	return states
}

// States возвращает состояние всех выходов для панели и центра.
func (o *Outputs) States() []HeaterState {
	if o == nil {
		return nil
	}
	states := make([]HeaterState, 0, len(o.list))
	for _, out := range o.list {
		states = append(states, out.State())
	}
	return states
}

// Close гасит все выходы и освобождает линии. Ошибка одного выхода не
// должна оставить остальные включёнными, поэтому закрываются все.
func (o *Outputs) Close() error {
	if o == nil {
		return nil
	}
	var first error
	for _, out := range o.list {
		if err := out.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
