// Package telemetry содержит модель предметной области и её инварианты.
//
// Предметная область — тренажёр: велосипеды с датчиками Холла на колёсах,
// лестница с подсчётом этажей, силовая установка с подъёмом груза,
// электромагниты, удерживающие падающие препятствия, и тепловая пушка.
// Скорость считает плата, падение препятствия и пушку включает оператор.
//
// Модуль общий для центра сбора и сборщика на объекте: формат на проводе
// описан ровно в одном месте, иначе звенья расходятся при первом же
// изменении протокола.
package telemetry

import (
	"errors"
	"net/netip"
	"time"
)

// EventKind — вид датаграммы. Устройство различает их полем "event":
// периодические показания, события клавиатуры и смена состояния реле.
type EventKind string

const (
	EventSensor   EventKind = ""         // очередное измерение
	EventKey      EventKind = "key"      // нажатие клавиши
	EventRelay    EventKind = "relay"    // смена состояния тепловой пушки
	EventObstacle EventKind = "obstacle" // препятствие сброшено или взведено
	EventCode     EventKind = "code"     // проверен код, набранный на стойке
	EventCamera   EventKind = "cam"      // событие камеры (приходит не от платы)

	// Ниже — черновик контракта новых тренажёров: лестницы и силовой
	// установки. Поля согласованы с центром и панелью, но прошивка их
	// пока не шлёт; данные для проверки даёт имитатор. До согласования
	// с изготовителем плат имена и единицы могут измениться.
	EventFloor EventKind = "floor" // пройден этаж на лестнице
	EventSet   EventKind = "set"   // завершён подход на силовой установке
)

// ErrInvalid — пакет разобран, но не проходит проверку инвариантов.
var ErrInvalid = errors.New("некорректная телеметрия")

// Telemetry — полезная нагрузка одной датаграммы.
type Telemetry struct {
	Event EventKind `json:"event,omitempty"`

	// Показания датчика Холла на колесе (Event == EventSensor).
	//
	// SpeedKMH — то, что видит инструктор; RPM и период оставлены как
	// исходные величины: по ним видно, что мерил датчик, когда скорость
	// выглядит неправдоподобно.
	SpeedKMH float64 `json:"speed_kmh,omitempty"`
	RPM      float64 `json:"rpm,omitempty"`
	PeriodUS uint32  `json:"period_us,omitempty"`
	Pulses   uint32  `json:"pulses,omitempty"`

	// Пройденный путь с момента старта заезда, метры.
	DistanceM float64 `json:"distance_m,omitempty"`

	// Показания BMP280.
	TempC   float64 `json:"temp_c,omitempty"`
	PressPa float64 `json:"press_pa,omitempty"`

	// Нажатая клавиша (Event == EventKey).
	Key string `json:"key,omitempty"`

	// Состояние тепловой пушки ("on"/"off"), причина и порог
	// (Event == EventRelay).
	State  string  `json:"state,omitempty"`
	Reason string  `json:"reason,omitempty"`
	LimitC float64 `json:"limit_c,omitempty"`

	// Препятствия: маска взведённых (бит на препятствие) и номер того,
	// с которым что-то произошло (Event == EventObstacle).
	//
	// Маска, а не список: препятствий единицы, а датаграмму формирует
	// плата без динамической памяти.
	Obstacles     uint32 `json:"obstacles,omitempty"`
	ObstacleCount uint8  `json:"obstacle_count,omitempty"`
	ObstacleIndex uint8  `json:"obstacle_index,omitempty"`
	ObstacleState string `json:"obstacle_state,omitempty"` // "armed" | "dropped"

	// Результат проверки кода со стойки (Event == EventCode).
	//
	// Сам код не передаётся никогда: вводят его на виду у людей,
	// и протокол не место для набранных цифр.
	CodeResult string `json:"code_result,omitempty"`

	// Событие камеры (Event == EventCamera).
	Camera string `json:"camera,omitempty"`

	// --- Лестница: прохождение этажей. ЧЕРНОВИК, см. EventFloor.
	//
	// Floor — этаж, на котором спортсмен сейчас; Floors — сколько
	// этажей пройдено за заход. Два поля, а не одно: при спуске
	// текущий этаж убывает, а зачёт прохождений продолжает расти.
	Floor     uint8  `json:"floor,omitempty"`
	Floors    uint16 `json:"floors,omitempty"`
	FloorDir  string `json:"floor_dir,omitempty"`  // "up" | "down"
	StepCount uint16 `json:"step_count,omitempty"` // ступеней с начала захода

	// --- Силовая установка: подъём груза подходами. ЧЕРНОВИК, см. EventSet.
	//
	// LoadKG — вес на установке; Reps — повторы в текущем подходе;
	// Sets — завершённые подходы. Вес передаётся в каждом пакете:
	// его меняют между подходами, и по одному событию смены его
	// не восстановить, если пакет потерялся.
	LoadKG float64 `json:"load_kg,omitempty"`
	Reps   uint16  `json:"reps,omitempty"`
	Sets   uint16  `json:"sets,omitempty"`

	// Время работы устройства с момента старта. Монотонно растёт;
	// падение значения означает перезапуск контроллера.
	UptimeMS uint32 `json:"uptime_ms"`
}

func (t Telemetry) IsSensorReading() bool { return t.Event == EventSensor }
func (t Telemetry) IsKeyEvent() bool      { return t.Event == EventKey }
func (t Telemetry) IsRelayEvent() bool    { return t.Event == EventRelay }
func (t Telemetry) IsCameraEvent() bool   { return t.Event == EventCamera }

// IsObstacleEvent — препятствие сброшено или взведено заново.
func (t Telemetry) IsObstacleEvent() bool { return t.Event == EventObstacle }

// IsCodeEvent — на стойке проверили код.
func (t Telemetry) IsCodeEvent() bool { return t.Event == EventCode }

// IsFloorEvent — на лестнице пройден этаж (черновик контракта).
func (t Telemetry) IsFloorEvent() bool { return t.Event == EventFloor }

// IsSetEvent — на силовой установке завершён подход (черновик контракта).
func (t Telemetry) IsSetEvent() bool { return t.Event == EventSet }

// ObstacleArmed сообщает, взведено ли препятствие с этим номером.
func (t Telemetry) ObstacleArmed(index uint8) bool {
	return t.Obstacles&(1<<index) != 0
}

// IsEvent сообщает, что это событие, а не очередное измерение. События
// хранятся вечно, показания стареют — отсюда разные коллекции в БД.
func (t Telemetry) IsEvent() bool { return t.Event != EventSensor }

// Uptime возвращает время работы устройства как длительность.
func (t Telemetry) Uptime() time.Duration {
	return time.Duration(t.UptimeMS) * time.Millisecond
}

// Validate проверяет инварианты, общие для всех источников данных.
func (t Telemetry) Validate() error {
	switch t.Event {
	case EventSensor, EventKey, EventRelay, EventObstacle, EventCode, EventCamera,
		EventFloor, EventSet:
	default:
		return ErrInvalid
	}
	if t.IsKeyEvent() && t.Key == "" {
		return ErrInvalid
	}
	if t.IsRelayEvent() && t.State == "" {
		return ErrInvalid
	}
	if t.IsCameraEvent() && t.Camera == "" {
		return ErrInvalid
	}
	if t.IsObstacleEvent() && t.ObstacleState == "" {
		return ErrInvalid
	}
	if t.IsFloorEvent() && t.FloorDir != "up" && t.FloorDir != "down" {
		return ErrInvalid
	}
	if t.IsCodeEvent() && t.CodeResult == "" {
		return ErrInvalid
	}
	// Отрицательная скорость означает ошибку счёта на плате, а не
	// движение назад: датчик Холла направление не различает.
	if t.SpeedKMH < 0 || t.RPM < 0 {
		return ErrInvalid
	}
	return nil
}

// DeviceID идентифицирует устройство. Присваивается сборщиком по карте
// из конфигурации: сама плата своего идентификатора не передаёт.
type DeviceID string

// Packet — принятая датаграмма вместе с метаданными приёма.
type Packet struct {
	Device   DeviceID
	Received time.Time
	From     netip.AddrPort
	Data     Telemetry
}

// Rebooted сообщает, что устройство перезапустилось между prev и p.
// Признак используют и сборщик (досылка уставок), и центр (журнал).
func (p Packet) Rebooted(prev Packet) bool {
	return p.Data.UptimeMS < prev.Data.UptimeMS
}

// EventName возвращает имя события для журналов и таблиц: у показаний
// датчика поле пустое, но в выдаче пустая строка неинформативна.
func (t Telemetry) EventName() string {
	if t.Event == EventSensor {
		return "sensor"
	}
	return string(t.Event)
}
