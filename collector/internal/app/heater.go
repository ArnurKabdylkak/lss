package app

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Тепловая пушка тренажёра: реле на выводе Raspberry Pi и защита от
// перегрева.
//
// Управление и защита живут в одном узле намеренно. Решение об
// отключении должен принимать тот, кто его исполняет: если бы защиту
// считала плата, а реле держал сборщик, обрыв связи между ними оставил
// бы пушку включённой.
//
// Температура берётся из двух источников — своего датчика RPi и датчика
// платы. Для защиты берётся МАКСИМУМ: перегрев хотя бы по одному
// датчику отключает пушку. Среднее замаскировало бы локальный нагрев,
// ведь датчики стоят в разных точках.

// HeaterReason — почему пушка в текущем состоянии.
type HeaterReason string

const (
	HeaterOK          HeaterReason = "ok"           // работает, температура в норме
	HeaterOvertemp    HeaterReason = "overtemp"     // отключена по превышению порога
	HeaterCooldown    HeaterReason = "cooldown"     // остыло, выдерживается пауза
	HeaterSensorFail  HeaterReason = "sensor_fail"  // нет достоверной температуры
	HeaterOperatorOff HeaterReason = "operator_off" // выключена оператором
)

// HeaterLimits — уставки защиты.
type HeaterLimits struct {
	// Порог отключения и температура возврата, °C.
	//
	// Гистерезис обязателен: без него реле щёлкает на каждом измерении
	// из-за шума датчика.
	LimitC   float64
	RestoreC float64

	// Минимальное время в отключённом состоянии.
	MinOff time.Duration

	// Насколько свежими считаются показания платы. Они идут по сети, и
	// устаревшие учитывать нельзя — но их отсутствие само по себе не
	// должно гасить исправный тренажёр.
	BoardTTL time.Duration

	// Расхождение датчиков, после которого это считается неисправностью
	// одного из них. Инструктор должен знать, а не получить усреднённую
	// неправду.
	DisagreeC float64
}

// HeaterState — что показывать инструктору.
//
// Состояние общее для всех выходов: у ключа без нагрева температурные
// поля просто пусты. Отдельный тип для него завёл бы в панели две почти
// одинаковых таблицы вместо одной.
type HeaterState struct {
	// Какой это выход и какого он рода. На объекте их несколько.
	ID   string     `json:"id"`
	Kind OutputKind `json:"kind"`

	On       bool         `json:"on"`
	Allowed  bool         `json:"allowed"`
	Reason   HeaterReason `json:"reason"`
	LimitC   float64      `json:"limit_c"`
	RestoreC float64      `json:"restore_c"`
	LocalC   *float64     `json:"local_c,omitempty"`   // датчик RPi
	BoardC   *float64     `json:"board_c,omitempty"`   // датчик платы
	DecidedC *float64     `json:"decided_c,omitempty"` // по чему принято решение
	Disagree bool         `json:"disagree"`            // датчики расходятся
	Since    time.Time    `json:"since"`
	Dry      bool         `json:"dry_run"` // реле недоступно, только учёт
}

// Switch — то, чем управляет пушка. Реализуется GPIOLine; отдельный
// интерфейс нужен, чтобы защиту можно было проверить без железа.
type Switch interface {
	Set(on bool) error
	Value() bool
}

// Heater — реле пушки с тепловой защитой.
type Heater struct {
	mu sync.Mutex

	// Идентификатор выхода, его линия и её имя для телеметрии.
	// Выставляются сборкой выходов; в конструкторе их нет, чтобы
	// проверять защиту можно было вообще без железа.
	id     string
	source string
	line   *GPIOLine

	sw     Switch
	limits HeaterLimits
	log    *slog.Logger

	on       bool
	allowed  bool
	reason   HeaterReason
	offSince time.Time
	since    time.Time

	local      *reading
	board      *reading
	disagree   bool
	lastDecide *float64

	// Было ли отключение по перегреву. Гистерезис — это правило
	// ВОЗВРАТА после срабатывания защиты, а не условие первого пуска:
	// иначе пушку нельзя было бы включить в тёплом помещении, где
	// температура уже выше точки возврата, хотя порога она не достигла.
	tripped bool
}

type reading struct {
	value float64
	at    time.Time
}

// NewHeater создаёт пушку в безопасном состоянии: выключена, работа не
// разрешена. Пока инструктор не включил её сознательно, тренажёр не
// греет.
func NewHeater(sw Switch, limits HeaterLimits, log *slog.Logger) *Heater {
	limits.applyDefaults()

	h := &Heater{
		sw:       sw,
		limits:   limits,
		log:      log,
		reason:   HeaterOperatorOff,
		offSince: time.Now(),
		since:    time.Now(),
	}
	if sw != nil {
		if err := sw.Set(false); err != nil {
			log.Error("не удалось снять реле пушки при запуске", "err", err)
		}
	}
	return h
}

// applyDefaults подставляет значения там, где их не задали.
//
// MinOff намеренно не трогается: ноль — законная настройка «без
// выдержки», и отличить её от «не задано» здесь невозможно. Умолчание
// для неё ставит загрузка конфигурации, которая видит, было ли поле
// в файле.
func (l *HeaterLimits) applyDefaults() {
	if l.LimitC <= 0 {
		l.LimitC = 60
	}
	if l.RestoreC <= 0 {
		l.RestoreC = l.LimitC - 10
	}
	if l.BoardTTL <= 0 {
		l.BoardTTL = 5 * time.Second
	}
	if l.DisagreeC <= 0 {
		l.DisagreeC = 5
	}
	if l.MinOff < 0 {
		l.MinOff = 0
	}
}

// SetLimits меняет уставки. Проверка здесь, а не только в центре:
// сборщик не полагается на валидацию, сделанную где-то ещё.
func (h *Heater) SetLimits(limitC, restoreC float64) error {
	if restoreC >= limitC {
		return fmt.Errorf("температура возврата (%.1f) должна быть ниже порога (%.1f)",
			restoreC, limitC)
	}
	if limitC <= 0 || limitC > 125 {
		return fmt.Errorf("порог %.1f вне диапазона датчика (0–125 °C)", limitC)
	}

	h.mu.Lock()
	h.limits.LimitC = limitC
	h.limits.RestoreC = restoreC
	h.mu.Unlock()

	h.log.Info("уставки тепловой защиты изменены", "limit_c", limitC, "restore_c", restoreC)
	h.Update()
	return nil
}

// Allow разрешает или запрещает работу пушки.
//
// Разрешение — это именно разрешение, а не принуждение: включит ли
// реле, решает защита по температуре. Запрет исполняется всегда.
func (h *Heater) Allow(allowed bool) HeaterState {
	h.mu.Lock()
	h.allowed = allowed
	h.mu.Unlock()

	return h.Update()
}

// ObserveLocal принимает показания своего датчика RPi.
func (h *Heater) ObserveLocal(tempC float64, at time.Time) {
	h.mu.Lock()
	h.local = &reading{value: tempC, at: at}
	h.mu.Unlock()
}

// ObserveBoard принимает показания датчика платы.
func (h *Heater) ObserveBoard(tempC float64, at time.Time) {
	h.mu.Lock()
	h.board = &reading{value: tempC, at: at}
	h.mu.Unlock()
}

// Update пересчитывает состояние. Вызывается по таймеру и после каждой
// команды: реакция на перегрев не должна ждать следующего опроса.
func (h *Heater) Update() HeaterState {
	h.mu.Lock()
	defer h.mu.Unlock()

	was, wasReason := h.on, h.reason
	now := time.Now()

	decided, ok := h.decideTemp(now)
	h.lastDecide = decided

	switch {
	case !h.allowed:
		// Запрет оператора исполняется всегда: снять нагрев безопасно
		// при любой температуре.
		h.on = false
		h.reason = HeaterOperatorOff

	case !ok:
		// Без достоверной температуры защита работать не может,
		// поэтому пушка снимается — это безопасное состояние.
		h.on = false
		h.reason = HeaterSensorFail

	case *decided >= h.limits.LimitC:
		h.on = false
		h.tripped = true
		h.reason = HeaterOvertemp

	case !h.on:
		// После срабатывания защиты — возврат только ниже температуры
		// восстановления и после выдержки: так реле не щёлкает на
		// границе порога. До первого срабатывания достаточно того, что
		// температура ниже порога.
		switch {
		case h.tripped && *decided > h.limits.RestoreC:
			h.reason = HeaterOvertemp
		case h.tripped && now.Sub(h.offSince) < h.limits.MinOff:
			h.reason = HeaterCooldown
		default:
			h.on = true
			h.tripped = false
			h.reason = HeaterOK
		}

	default:
		h.reason = HeaterOK
	}

	if was && !h.on {
		h.offSince = now
	}
	if was != h.on {
		h.since = now
		h.apply()
	}
	if was != h.on || wasReason != h.reason {
		h.log.Info("тепловая пушка", "on", h.on, "reason", h.reason,
			"decided_c", fmtTemp(decided))
	}
	h.log.Debug("пересчёт защиты", "on", h.on, "reason", h.reason,
		"decided_c", fmtTemp(decided), "board", h.board != nil,
		"local", h.local != nil)

	return h.stateLocked()
}

// decideTemp выбирает температуру, по которой принимается решение.
// Вызывается под мьютексом.
func (h *Heater) decideTemp(now time.Time) (*float64, bool) {
	var values []float64

	if h.local != nil {
		values = append(values, h.local.value)
	}
	// Показания платы идут по сети: устаревшие игнорируем, но их
	// отсутствие не гасит тренажёр, если свой датчик исправен.
	boardFresh := h.board != nil && now.Sub(h.board.at) <= h.limits.BoardTTL
	if boardFresh {
		values = append(values, h.board.value)
	}

	h.disagree = false
	if h.local != nil && boardFresh {
		h.disagree = math.Abs(h.local.value-h.board.value) > h.limits.DisagreeC
	}

	if len(values) == 0 {
		return nil, false
	}

	// Максимум, а не среднее: датчики стоят в разных точках, и
	// перегрев хотя бы в одной — повод выключить.
	worst := values[0]
	for _, v := range values[1:] {
		if v > worst {
			worst = v
		}
	}
	return &worst, true
}

// apply переносит решение на реле. Вызывается под мьютексом.
func (h *Heater) apply() {
	if h.sw == nil {
		return // реле недоступно: считаем, но не управляем
	}
	if err := h.sw.Set(h.on); err != nil {
		h.log.Error("реле пушки не переключилось", "on", h.on, "err", err)
	}
}

// State возвращает текущее состояние.
func (h *Heater) State() HeaterState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stateLocked()
}

func (h *Heater) ID() string       { return h.id }
func (h *Heater) Kind() OutputKind { return OutputHeater }
func (h *Heater) Source() string   { return h.source }

// Close гасит нагрев и освобождает линию. Освобождение само снимает
// вывод в безопасное состояние, но полагаться только на это нельзя:
// линии может не быть вовсе, а гасить всё равно надо.
func (h *Heater) Close() error {
	h.Allow(false)
	if h.line != nil {
		return h.line.Close()
	}
	return nil
}

func (h *Heater) stateLocked() HeaterState {
	st := HeaterState{
		ID:       h.id,
		Kind:     OutputHeater,
		On:       h.on,
		Allowed:  h.allowed,
		Reason:   h.reason,
		LimitC:   h.limits.LimitC,
		RestoreC: h.limits.RestoreC,
		Disagree: h.disagree,
		Since:    h.since,
		Dry:      h.sw == nil,
		DecidedC: h.lastDecide,
	}
	if h.local != nil {
		v := h.local.value
		st.LocalC = &v
	}
	if h.board != nil {
		v := h.board.value
		st.BoardC = &v
	}
	return st
}

func fmtTemp(v *float64) string {
	if v == nil {
		return "нет данных"
	}
	return fmt.Sprintf("%.2f", *v)
}
