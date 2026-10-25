// Package layout — устройство объекта: какие комнаты есть, что в каждой
// стоит и какие камеры её снимают.
//
// Раскладку задаёт инженер на месте, а не код: комнат три, состав
// тренажёров меняется, и панель должна показывать объект так, как он
// устроен в жизни, а не плоским списком устройств.
package layout

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

// ErrInvalidLayout — раскладка объекта описана неверно.
var ErrInvalidLayout = errors.New("некорректная раскладка объекта")

// RoomID — идентификатор комнаты; попадает в адреса API.
type RoomID string

// Kind — вид тренажёра. Определяет, какие показания у устройства
// осмысленны и как панель его рисует.
type Kind string

const (
	// Тренажёры и задания: дают показания или отметку о прохождении.
	KindBike   Kind = "bike"   // велоэргометр, датчик Холла на колесе
	KindStairs Kind = "stairs" // бесконечная лестница, подсчёт ступеней
	KindLoad   Kind = "load"   // ударный молот, подъём груза
	KindStand  Kind = "stand"  // панель набора кода (задание «Набор чисел»)
	KindMaze   Kind = "maze"   // лабиринт: кнопка на выходе разблокирует дверь
	KindFire   Kind = "fire"   // имитатор очага: кнопка отключения
	KindRubble Kind = "rubble" // завалы: электромагниты отпускают трубы
	KindDoor   Kind = "door"   // тренировочная дверь со сменным ригелем

	// Оборудование комнаты: управляется, но показаний не даёт.
	// Висит на выводах сборщика этой комнаты.
	KindHeater Kind = "heater" // тепловая пушка с защитой по температуре
	KindLight  Kind = "light"  // светильники
	KindFan    Kind = "fan"    // вытяжная вентиляция
	KindSmoke  Kind = "smoke"  // дымогенератор
	KindStrobe Kind = "strobe" // стробоскоп и мигающие прожекторы
	KindSound  Kind = "sound"  // акустическая система
	KindLock   Kind = "lock"   // электромагнитный замок двери
)

// Title возвращает название вида по-русски — для панели и журналов.
func (k Kind) Title() string {
	switch k {
	case KindBike:
		return "велоэргометр"
	case KindStairs:
		return "бесконечная лестница"
	case KindLoad:
		return "ударный молот"
	case KindStand:
		return "панель набора кода"
	case KindMaze:
		return "лабиринт"
	case KindFire:
		return "имитатор очага"
	case KindRubble:
		return "завалы"
	case KindDoor:
		return "тренировочная дверь"
	case KindHeater:
		return "тепловая пушка"
	case KindLight:
		return "светильники"
	case KindFan:
		return "вытяжная вентиляция"
	case KindSmoke:
		return "дымогенератор"
	case KindStrobe:
		return "стробоскоп"
	case KindSound:
		return "акустическая система"
	case KindLock:
		return "электромагнитный замок"
	default:
		return string(k)
	}
}

// Known сообщает, что вид устройства известен центру.
func (k Kind) Known() bool {
	switch k {
	case KindBike, KindStairs, KindLoad, KindStand, KindMaze, KindFire, KindRubble, KindDoor,
		KindHeater, KindLight, KindFan, KindSmoke, KindStrobe, KindSound, KindLock:
		return true
	default:
		return false
	}
}

// Trainer сообщает, что это тренажёр с показаниями, на который
// назначают спасателя поимённо (сценарий теплокамеры §4).
//
// Задания дымокамеры сюда не входят: их проходят звеном, и результат
// связывается с карточками всех участников сразу.
func (k Kind) Trainer() bool {
	switch k {
	case KindBike, KindStairs, KindLoad:
		return true
	default:
		return false
	}
}

// Equipment сообщает, что это оборудование комнаты, а не тренажёр:
// им управляют, но показаний оно не даёт и в зачёт не идёт.
func (k Kind) Equipment() bool {
	switch k {
	case KindHeater, KindLight, KindFan, KindSmoke, KindStrobe, KindSound, KindLock:
		return true
	default:
		return false
	}
}

// Draft сообщает, что контракт этого вида ещё не согласован с
// изготовителем плат: данные даёт имитатор, формат может измениться.
func (k Kind) Draft() bool {
	switch k {
	case KindStairs, KindLoad, KindMaze, KindFire, KindRubble, KindDoor:
		return true
	default:
		return false
	}
}

// Device — тренажёр в комнате.
type Device struct {
	ID   string // идентификатор, который проставляет сборщик
	Name string
	Kind Kind

	// StepsPerFloor — сколько ступеней считать этажом на лестнице
	// (сценарий §5: правило пересчёта задаётся в настройках).
	// Ноль — пересчёт не делается, берётся то, что шлёт плата.
	StepsPerFloor int
}

// Floors пересчитывает ступени в этажи по правилу тренажёра.
//
// Второй аргумент — то, что насчитала сама плата: если правило в
// настройках не задано, верим ей.
func (d Device) Floors(steps, reported uint16) uint16 {
	if d.StepsPerFloor <= 0 {
		return reported
	}
	return steps / uint16(d.StepsPerFloor)
}

// Title — как называть устройство на экране.
func (d Device) Title() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Kind.Title()
}

// Heater — уставки защиты тепловой пушки комнаты.
//
// Предел держится рядом с комнатой, а не в коде: в комнате с лестницей
// и грузом воздух прогревается иначе, чем там, где крутят педали.
type Heater struct {
	Device  string  // устройство пушки
	LimitC  float64 // порог отключения
	ResumeC float64 // температура возврата
}

// Set сообщает, что пушка в комнате есть.
func (h Heater) Set() bool { return h.Device != "" }

// Validate проверяет, что уставки осмысленны.
func (h Heater) Validate(room RoomID) error {
	if !h.Set() {
		return nil
	}
	if h.LimitC <= 0 {
		return fmt.Errorf("%w: комната %s: не задан порог отключения пушки", ErrInvalidLayout, room)
	}
	// Возврат ниже порога — иначе защита, сняв нагрев, тут же включит
	// его обратно и будет щёлкать реле до отказа.
	if h.ResumeC >= h.LimitC {
		return fmt.Errorf("%w: комната %s: температура возврата (%.1f) должна быть ниже порога (%.1f)",
			ErrInvalidLayout, room, h.ResumeC, h.LimitC)
	}
	return nil
}

// Room — комната объекта со всем, что в ней стоит.
type Room struct {
	ID      RoomID
	Name    string
	Devices []Device
	Heater  Heater
	Prepare Prepare
	Tasks   []Task
	Cameras []video.Camera

	// Capacity — сколько человек помещается в комнату одновременно.
	// Ноль — не ограничено явно, применяется DefaultCapacity.
	Capacity int

	// Collector — устройство сборщика этой комнаты. Ему адресуется
	// аварийная остановка: он знает свою обвязку и переводит выходы
	// в безопасное состояние сам.
	Collector string

	// Floor — этаж внутри секции. Ноль — секция из одной комнаты, и
	// этаж называть незачем. Нужен панели: дымокамера показывается
	// одним помещением, а оборудование и задания — по этажам.
	Floor int

	// Equipment — узлы оборудования помещения вместе с их ролью в
	// сценарии. Пусто — берётся набор по виду помещения.
	Equipment []UnitSpec

	// Sensor — устройство, с которого берётся температура помещения.
	// Пусто — берётся с тепловой пушки. В дымокамере пушки может не
	// быть, а температуру показывать надо (сценарий дымокамеры §1).
	Sensor string
}

// TempSource возвращает устройство, по которому показывают
// температуру комнаты.
func (r Room) TempSource() string {
	if r.Sensor != "" {
		return r.Sensor
	}
	return r.Heater.Device
}

// Unit — узел оборудования помещения.
//
// Здесь же и его роль в сценарии: что включается при старте и каким
// узел становится по аварии. Решение принимает центр, а сборщик
// только щёлкает выводами — иначе сценарий жил бы в трёх прошивках
// сразу и расходился бы с панелью.
type Unit struct {
	ID   string // smoke, flood, strobe, light, fan, sound, heat, stairs…
	Name string

	// Prepare — включается ли узел при старте подготовки.
	Prepare bool

	// Hold и Cycle — см. UnitSpec.
	Hold  time.Duration
	Cycle time.Duration

	// Alarm — каким узел должен стать по аварии: on, off или keep.
	// Пусто означает off: обесточить безопаснее, чем оставить.
	Alarm string
}

// AlarmOn сообщает, что узел по аварии включается.
func (u Unit) AlarmOn() bool { return u.Alarm == "on" }

// AlarmKeep сообщает, что узел по аварии не трогают. Так заданы
// магниты «Завалов»: их цепь независима от дверных замков, и
// аварийное открытие дверей не должно ронять трубы.
func (u Unit) AlarmKeep() bool { return u.Alarm == "keep" }

// UnitSound — узел звука. В отличие от прочих, за ним нет реле:
// дорожку крутит сам центр, у него акустика и записи.
const UnitSound = "sound"

// UnitSpec — узел в раскладке объекта: чем он является и как ведёт
// себя в сценарии.
type UnitSpec struct {
	Unit    string
	Prepare bool
	Alarm   string

	// Hold — сколько узел остаётся включённым, после чего центр сам
	// его выключает. Ноль — выключает оператор.
	//
	// Нужно там, где нагрузка не рассчитана на длительную работу:
	// электромагниты «Завалов» держат трубы током и, оставшись
	// включёнными, сгорают. Выдержка живёт в раскладке, а не в
	// прошивке сборщика: это свойство монтажа, и инженер на объекте
	// правит его там же, где описывает сам узел.
	Hold time.Duration

	// Cycle — период автоматического переключения, пока помещение
	// готовится или идёт занятие. Ноль — узел переключает человек.
	//
	// Так работают прожекторы: мигание сбивает ориентацию в дыму.
	// Ручное переключение имеет приоритет и автоматику по этому узлу
	// снимает до конца цикла комнаты.
	Cycle time.Duration
}

// Units — узлы помещения. Пока состав оборудования в раскладке не
// расписан, берётся набор по виду помещения: он одинаков для всех
// дымокамер и для всех теплокамер объекта.
func (r Room) Units(section string) []Unit {
	if len(r.Equipment) > 0 {
		out := make([]Unit, 0, len(r.Equipment))
		for _, u := range r.Equipment {
			out = append(out, Unit{
				ID: u.Unit, Name: unitNames[u.Unit],
				Prepare: u.Prepare, Alarm: u.Alarm,
				Hold: u.Hold, Cycle: u.Cycle,
			})
		}
		return out
	}

	ids := smokeUnits
	if r.Heater.Set() {
		ids = heatUnits
	}
	out := make([]Unit, 0, len(ids))
	for _, id := range ids {
		out = append(out, Unit{ID: id, Name: unitNames[id]})
	}
	return out
}

// HasUnit сообщает, что такой узел в помещении есть.
func (r Room) HasUnit(id string) bool {
	for _, u := range r.Units("") {
		if u.ID == id {
			return true
		}
	}
	return false
}

var (
	// Теплокамера: пушка, свет, вытяжка.
	heatUnits = []string{"heat", "light", "fan"}
	// Дымокамера: дым, прожекторы, стробоскоп, звук, свет, вытяжка.
	smokeUnits = []string{"smoke", "flood", "strobe", "sound", "light", "fan"}

	unitNames = map[string]string{
		"lock":   "Замки дверей",
		"rubble": "Завалы",
		"aux":    "Резерв 1",
		"aux2":   "Резерв 2",
		"aux3":   "Резерв 3",
		"aux4":   "Резерв 4",
		"smoke":  "Дымогенератор",
		"flood":  "Прожекторы",
		"strobe": "Стробоскоп",
		"light":  "Освещение",
		"fan":    "Вытяжка",
		"sound":  "Аудиоэффекты",
		"heat":   "Тепловая пушка",
		"stairs": "Бесконечная лестница",
	}
)

// Task — задание маршрута: то, что команда должна пройти.
//
// Проходит задание команда целиком, а не отдельный спасатель: отметка
// ставится одна и связывается с карточками всех участников.
type Task struct {
	ID   string
	Name string

	// Device — устройство, по сигналу которого задание отмечается
	// само. Пусто — отмечает администратор вручную.
	Device string
	// Note — пояснение для экрана: что именно надо сделать.
	Note string
}

// Auto сообщает, что задание отмечается по сигналу устройства.
func (t Task) Auto() bool { return t.Device != "" }

// Task возвращает задание комнаты по идентификатору.
func (r Room) Task(id string) (Task, bool) {
	for _, t := range r.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// DefaultCapacity — вместимость по умолчанию: сценарий говорит о
// 2–3 спасателях одновременно.
const DefaultCapacity = 3

// Limit возвращает вместимость комнаты с учётом умолчания.
func (r Room) Limit() int {
	if r.Capacity > 0 {
		return r.Capacity
	}
	return DefaultCapacity
}

// Trainers возвращает тренажёры комнаты — те, на которые назначают
// спасателя поимённо.
func (r Room) Trainers() []Device {
	out := make([]Device, 0, len(r.Devices))
	for _, d := range r.Devices {
		if d.Kind.Trainer() {
			out = append(out, d)
		}
	}
	return out
}

// Device возвращает устройство комнаты по идентификатору.
func (r Room) Device(id string) (Device, bool) {
	for _, d := range r.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// Title — название комнаты для экрана.
func (r Room) Title() string {
	if r.Name != "" {
		return r.Name
	}
	return string(r.ID)
}

// DeviceIDs возвращает идентификаторы всех устройств комнаты,
// включая тепловую пушку.
func (r Room) DeviceIDs() []string {
	out := make([]string, 0, len(r.Devices)+1)
	for _, d := range r.Devices {
		out = append(out, d.ID)
	}
	if r.Heater.Set() {
		out = append(out, r.Heater.Device)
	}
	return out
}

// Validate проверяет описание комнаты целиком.
func (r Room) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: у комнаты нет идентификатора", ErrInvalidLayout)
	}
	if !safeID(string(r.ID)) {
		return fmt.Errorf("%w: идентификатор комнаты %q — допустимы латиница, цифры, «-» и «_»",
			ErrInvalidLayout, r.ID)
	}

	seen := make(map[string]struct{}, len(r.Devices))
	for _, d := range r.Devices {
		if d.ID == "" {
			return fmt.Errorf("%w: комната %s: устройство без идентификатора", ErrInvalidLayout, r.ID)
		}
		if !d.Kind.Known() {
			return fmt.Errorf("%w: комната %s: неизвестный вид тренажёра %q у %s",
				ErrInvalidLayout, r.ID, d.Kind, d.ID)
		}
		if _, dup := seen[d.ID]; dup {
			return fmt.Errorf("%w: комната %s: устройство %s описано дважды", ErrInvalidLayout, r.ID, d.ID)
		}
		seen[d.ID] = struct{}{}
	}

	return r.Heater.Validate(r.ID)
}

// Section — секция комплекса: одна тренировка на все её комнаты.
//
// Теплокамера — одна комната, дымокамера — две: команда проходит их
// подряд, но таймер, видеозапись и зачёт у неё общие (сценарий
// дымокамеры §3). Поэтому занятие открывается на секцию.
type Section struct {
	ID    string
	Name  string
	Rooms []RoomID
}

// Multi сообщает, что секция собрана из нескольких помещений. Для
// занятия такая секция — одна комната: этажи дымокамеры проходят
// подряд, а состав, таймер и зачёт у звена общие.
func (s Section) Multi() bool { return len(s.Rooms) > 1 }

// Primary возвращает первую комнату секции — с неё начинается
// маршрут, и на неё записывается занятие.
func (s Section) Primary() RoomID {
	if len(s.Rooms) == 0 {
		return ""
	}
	return s.Rooms[0]
}

// Has сообщает, что комната входит в секцию.
func (s Section) Has(id RoomID) bool {
	for _, r := range s.Rooms {
		if r == id {
			return true
		}
	}
	return false
}

// Object — объект целиком: его комнаты и секции.
type Object struct {
	Site     string
	Rooms    []Room
	Sections []Section
}

// SectionOf возвращает секцию, в которую входит комната.
//
// Комната без секции считается секцией из самой себя: так объект без
// описанных секций работает по-прежнему.
func (o Object) SectionOf(id RoomID) Section {
	for _, s := range o.Sections {
		if s.Has(id) {
			return s
		}
	}
	return Section{ID: string(id), Name: string(id), Rooms: []RoomID{id}}
}

// RoomsOf возвращает комнаты секции в порядке маршрута.
func (o Object) RoomsOf(s Section) []Room {
	out := make([]Room, 0, len(s.Rooms))
	for _, id := range s.Rooms {
		for _, r := range o.Rooms {
			if r.ID == id {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// Validate проверяет объект: комнаты, их содержимое и то, что одно и то
// же устройство или камера не приписаны к двум комнатам сразу.
func (o Object) Validate() error {
	// Секции: комната не может входить в две сразу, а несуществующая
	// комната в секции означает опечатку в раскладке.
	inSection := make(map[RoomID]string, len(o.Rooms))
	for _, sec := range o.Sections {
		if sec.ID == "" {
			return fmt.Errorf("%w: у секции нет идентификатора", ErrInvalidLayout)
		}
		if len(sec.Rooms) == 0 {
			return fmt.Errorf("%w: в секции %s нет комнат", ErrInvalidLayout, sec.ID)
		}
		for _, id := range sec.Rooms {
			if other, dup := inSection[id]; dup {
				return fmt.Errorf("%w: комната %s входит и в %s, и в %s",
					ErrInvalidLayout, id, other, sec.ID)
			}
			inSection[id] = sec.ID

			var known bool
			for _, r := range o.Rooms {
				if r.ID == id {
					known = true
					break
				}
			}
			if !known {
				return fmt.Errorf("%w: в секции %s указана неизвестная комната %s",
					ErrInvalidLayout, sec.ID, id)
			}
		}
	}

	rooms := make(map[RoomID]struct{}, len(o.Rooms))
	devices := make(map[string]RoomID)
	cameras := make(map[video.CameraID]RoomID)

	for _, r := range o.Rooms {
		if err := r.Validate(); err != nil {
			return err
		}
		if _, dup := rooms[r.ID]; dup {
			return fmt.Errorf("%w: комната %s описана дважды", ErrInvalidLayout, r.ID)
		}
		rooms[r.ID] = struct{}{}

		for _, id := range r.DeviceIDs() {
			if other, dup := devices[id]; dup {
				return fmt.Errorf("%w: устройство %s приписано и к %s, и к %s",
					ErrInvalidLayout, id, other, r.ID)
			}
			devices[id] = r.ID
		}

		for _, c := range r.Cameras {
			if err := c.Validate(); err != nil {
				return err
			}
			if other, dup := cameras[c.ID]; dup {
				return fmt.Errorf("%w: камера %s приписана и к %s, и к %s",
					ErrInvalidLayout, c.ID, other, r.ID)
			}
			cameras[c.ID] = r.ID
		}
	}
	return nil
}

// Cameras собирает камеры всех комнат — в таком виде их ждёт видеотракт.
func (o Object) Cameras() []video.Camera {
	var out []video.Camera
	for _, r := range o.Rooms {
		out = append(out, r.Cameras...)
	}
	return out
}

// RoomOfDevice сообщает, в какой комнате стоит устройство.
func (o Object) RoomOfDevice(id string) (Room, bool) {
	for _, r := range o.Rooms {
		for _, d := range r.DeviceIDs() {
			if d == id {
				return r, true
			}
		}
	}
	return Room{}, false
}

func safeID(s string) bool {
	if s == "" || len(s) > 64 || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// CollectorOf возвращает сборщик, к которому подключено устройство.
//
// На объекте сборщиков несколько, а очередь команд общая: без этого
// соответствия команда ушла бы тому, кто первым спросил, и щёлкнула
// бы не тем реле — или не щёлкнула вовсе.
func (o Object) CollectorOf(device string) (string, bool) {
	for _, r := range o.Rooms {
		if r.Collector == device {
			return r.Collector, true
		}
		if r.Collector == "" {
			continue
		}
		if r.Heater.Device == device || r.Sensor == device {
			return r.Collector, true
		}
		for _, d := range r.Devices {
			if d.ID == device {
				return r.Collector, true
			}
		}
		for _, t := range r.Tasks {
			if t.Device == device {
				return r.Collector, true
			}
		}
	}
	return "", false
}
