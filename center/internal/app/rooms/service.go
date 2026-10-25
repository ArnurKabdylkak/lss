// Package rooms собирает комнату целиком: что в ней стоит, что эти
// устройства сейчас показывают и какие камеры её снимают.
//
// Панели нужен объект таким, каков он в жизни: комната, в ней
// велосипед, лестница, силовая установка, пушка и две камеры. Плоский
// список устройств заставляет дежурного держать раскладку в голове.
package rooms

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/app/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Exercises — упражнения комнаты: кто что делает на тренажёрах
// прямо сейчас. Интерфейс объявлен здесь, чтобы комнаты не зависели
// от сценариев занятий напрямую.
type Exercises interface {
	Exercises(ctx context.Context, room layout.RoomID) ([]training.Exercise, error)
	// Tasks — задания маршрута с отметками о прохождении.
	Tasks(ctx context.Context, room layout.RoomID) ([]training.Task, error)
	// Rotations — сколько раз звено сменило тренажёры за занятие.
	Rotations(ctx context.Context, room layout.RoomID) int
}

// Streams — то, что прикладному слою нужно знать о видеотракте.
// Интерфейс объявлен здесь, чтобы комнаты не зависели от надзора
// за трансляциями напрямую.
type Streams interface {
	States() []video.State
	PlaylistURL(id video.CameraID) string
}

// Silence — через сколько молчания устройство считается пропавшим.
// То же правило, что у приёмного тракта: панель и журнал должны
// называть «нет связи» в одно и то же время.
const Silence = 10 * time.Second

// DeviceView — тренажёр вместе с последним, что от него пришло.
type DeviceView struct {
	Device layout.Device
	Last   telemetry.Packet
	Seen   bool // приходило ли от него хоть что-то
	Stale  bool // связь потеряна
}

// CameraView — камера комнаты и её трансляция.
type CameraView struct {
	State    video.State
	Playlist string // пусто, когда смотреть нечего
}

// RoomView — комната целиком: всё, что нужно главному экрану.
type RoomView struct {
	Room layout.Room
	// Section — секция, в которую входит комната, и как она
	// называется целиком. Дымокамера показывается одним окном с
	// двумя этажами, и панели нужно имя секции, а не комнаты.
	Section     string
	SectionName string
	// SectionRooms — сколько помещений в секции.
	SectionRooms int
	State        layout.RoomState
	Devices      []DeviceView
	Heater       *DeviceView // nil, если пушки в комнате нет
	Cameras      []CameraView

	// TempC — температура воздуха в комнате; берётся с датчика пушки.
	TempC float64
	// HasTemp — есть ли достоверная температура: ноль градусов и
	// «датчик молчит» на экране выглядят одинаково, если не различать.
	HasTemp bool

	// Faults — неисправности: пропавшие тренажёры, упавшие камеры,
	// перегрев. Показываются на экране, а не только в журнале.
	Faults []Fault

	// Exercises — что делают на тренажёрах сейчас: показатели каждого
	// спасателя отдельно, его время и выполнение задания.
	Exercises []training.Exercise

	// Rotations — сколько раз звено сменило тренажёры. Круг двигает
	// центр сам, и оператор должен видеть, что это произошло.
	Rotations int

	// Tasks — задания маршрута **этого** помещения с отметками.
	// Задание проходит команда целиком.
	Tasks []training.Task

	// Route — ход всего маршрута секции: у дымокамеры он идёт через
	// две комнаты, и в окне каждой полезно знать, сколько пройдено
	// всего. Чужие задания при этом списком не показываются.
	Route Route
}

// Route — сколько заданий маршрута пройдено по всей секции.
type Route struct {
	Done  int
	Total int
}

// Commands — включение нагрева при старте подготовки. Функция, а не
// интерфейс: комнатам нужно ровно одно действие, и тащить ради него
// зависимость на весь командный тракт незачем.
type Commands func(ctx context.Context, room layout.Room, author string) error

// AlarmCommands — перевод оборудования комнаты в безопасное
// состояние. Команда адресуется сборщику: он знает свою обвязку и
// разом включает светильники и вытяжку, отпускает замки, снимает
// нагрев, дым, стробоскопы и звук, запускает оповещение.
type AlarmCommands func(ctx context.Context, room layout.Room, author string, on bool) error

// PrepareCommands — включение оборудования подготовки. Команда идёт
// сборщику комнаты: состав оборудования у теплокамеры и дымокамеры
// разный, и знать его должен тот, к кому оно подключено.
type PrepareCommands func(ctx context.Context, room layout.Room, author string, on bool) error

// CodeCommands — отправка кода задания на панель набора.
type CodeCommands func(ctx context.Context, room layout.Room, device, code, author string) error

// FireCommands — зажечь или потушить имитатор очага.
//
// Очаг горит постоянно и гаснет кнопкой — тем, кто его нашёл. Перед
// следующим заходом его зажигают заново, и делает это подготовка, а
// не человек в зале.
type FireCommands func(ctx context.Context, room layout.Room, device string, on bool, author string) error

// DeviceActions — разовые действия оборудования комнаты: отпустить
// трубы механизма «Завалы», воспроизвести сообщение.
type DeviceActions interface {
	Rubble(ctx context.Context, room layout.Room, author string) error
	Announce(ctx context.Context, room layout.Room, sound, author string) error
}

// FinishSession — закрытие занятия с причиной. Через него авария
// останавливает учёт: там же останавливается видеозапись, и двух
// путей закрытия занятия быть не должно.
//
// alarm отличает аварийную остановку от решения администратора: в
// карточке остаётся отметка «Аварийная остановка» (сценарий §7).
type FinishSession func(ctx context.Context, room layout.RoomID, reason string, alarm bool) error

type Service struct {
	// mu делает изменение состояния комнаты атомарным. Состояние
	// меняют несколько горутин — запросы панели и пульта,
	// подтверждения сборщиков, автоматика, срок ожидания, — и каждая
	// читает запись, правит копию и сохраняет её целиком. Без общей
	// блокировки сохранивший последним затирал чужое: например,
	// только что пришедшее подтверждение узла.
	mu sync.Mutex

	layout    port.LayoutRepository
	state     port.StateRepository
	states    port.RoomStateRepository
	sessions  port.SessionRepository
	streams   Streams
	commands  Commands
	prepares  PrepareCommands
	codes     CodeCommands
	fires     FireCommands
	actions   DeviceActions
	alarms    AlarmCommands
	journal   port.AlarmJournal
	finish    FinishSession
	exercises Exercises
	units     UnitCommands
	log       *slog.Logger
}

func New(l port.LayoutRepository, state port.StateRepository,
	states port.RoomStateRepository, sessions port.SessionRepository,
	streams Streams, commands Commands, prepares PrepareCommands,
	codes CodeCommands, actions DeviceActions, alarms AlarmCommands,
	journal port.AlarmJournal, finish FinishSession,
	exercises Exercises, log *slog.Logger) *Service {

	return &Service{
		layout: l, state: state, states: states, sessions: sessions,
		streams: streams, commands: commands, prepares: prepares,
		codes: codes, actions: actions, alarms: alarms,
		journal: journal, finish: finish, exercises: exercises, log: log,
	}
}

// WithFires назначает команду очагам. Отдельно от сборки сервиса:
// очаги появились позже, и ломать ради них подпись конструктора,
// которую зовут из одного места, незачем.
func (s *Service) WithFires(f FireCommands) { s.fires = f }

// WithUnits подключает ручное управление оборудованием. Отдельно от
// конструктора: это дополнение к автоматике подготовки, и без него
// комнаты работают как раньше.
func (s *Service) WithUnits(u UnitCommands) { s.units = u }

// Rooms возвращает все комнаты объекта в порядке из конфигурации.
func (s *Service) Rooms(ctx context.Context) ([]RoomView, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return nil, err
	}

	states := s.cameraStates()

	out := make([]RoomView, 0, len(obj.Rooms))
	for _, room := range obj.Rooms {
		view, err := s.compose(ctx, room, states)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// room возвращает описание комнаты из раскладки.
func (s *Service) room(ctx context.Context, id layout.RoomID) (layout.Room, bool, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return layout.Room{}, false, err
	}
	for _, r := range obj.Rooms {
		if r.ID == id {
			return r, true, nil
		}
	}
	return layout.Room{}, false, nil
}

// Room возвращает одну комнату.
func (s *Service) Room(ctx context.Context, id layout.RoomID) (RoomView, bool, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return RoomView{}, false, err
	}

	for _, room := range obj.Rooms {
		if room.ID != id {
			continue
		}
		view, err := s.compose(ctx, room, s.cameraStates())
		return view, err == nil, err
	}
	return RoomView{}, false, nil
}

func (s *Service) compose(ctx context.Context, room layout.Room,
	states map[video.CameraID]video.State) (RoomView, error) {

	view := RoomView{Room: room, Devices: make([]DeviceView, 0, len(room.Devices))}

	for _, d := range room.Devices {
		dv, err := s.device(ctx, d)
		if err != nil {
			return RoomView{}, err
		}
		view.Devices = append(view.Devices, dv)
	}

	if room.Heater.Set() {
		dv, err := s.device(ctx, layout.Device{
			ID:   room.Heater.Device,
			Kind: layout.KindHeater,
		})
		if err != nil {
			return RoomView{}, err
		}
		view.Heater = &dv
	}

	st, err := s.State(ctx, room.ID)
	if err != nil {
		return RoomView{}, err
	}
	view.State = st

	if obj, err := s.layout.Object(ctx); err == nil {
		section := obj.SectionOf(room.ID)
		view.Section = section.ID
		view.SectionName = section.Name
		view.SectionRooms = len(section.Rooms)
	}

	for _, cam := range room.Cameras {
		cv := CameraView{State: video.State{Camera: cam, Status: video.StatusStopped}}
		if st, ok := states[cam.ID]; ok {
			cv.State = st
		}
		// Ссылку даём, только когда смотреть есть что: иначе плеер
		// панели будет биться о несуществующий плейлист.
		if cv.State.Live() && s.streams != nil {
			cv.Playlist = s.streams.PlaylistURL(cam.ID)
		}
		view.Cameras = append(view.Cameras, cv)
	}

	// Температура комнаты — с назначенного датчика. Обычно это
	// тепловая пушка, но в дымокамере её может не быть, а температуру
	// показывать надо.
	if src := room.TempSource(); src != "" {
		last, seen, err := s.state.Latest(ctx, telemetry.DeviceID(src))
		if err != nil {
			return RoomView{}, err
		}
		if seen && time.Since(last.Received) <= Silence && last.Data.TempC != 0 {
			view.TempC = last.Data.TempC
			view.HasTemp = true
		}
	}
	// Достижение цели отмечается здесь же: показания уже прочитаны,
	// и второй источник правды о температуре заводить незачем.
	view.State = s.trackPreparation(ctx, view.State, view.TempC, view.HasTemp)
	view.Faults = s.faults(room, view.Devices, view.Heater, view.Cameras)

	if s.exercises != nil {
		if len(room.Trainers()) > 0 {
			ex, err := s.exercises.Exercises(ctx, room.ID)
			if err != nil {
				return RoomView{}, err
			}
			view.Exercises = ex
			view.Rotations = s.exercises.Rotations(ctx, room.ID)
		}
		if len(room.Tasks) > 0 {
			tasks, err := s.exercises.Tasks(ctx, room.ID)
			if err != nil {
				return RoomView{}, err
			}
			// В окне помещения — его собственные задания: чужие
			// только занимали бы экран и путали, кто где идёт.
			for _, t := range tasks {
				view.Route.Total++
				if t.Done {
					view.Route.Done++
				}
				if t.Room == room.ID {
					view.Tasks = append(view.Tasks, t)
				}
			}
		}
	}

	return view, nil
}

func (s *Service) device(ctx context.Context, d layout.Device) (DeviceView, error) {
	last, ok, err := s.state.Latest(ctx, telemetry.DeviceID(d.ID))
	if err != nil {
		return DeviceView{}, err
	}
	return DeviceView{
		Device: d,
		Last:   last,
		Seen:   ok,
		Stale:  ok && time.Since(last.Received) > Silence,
	}, nil
}

func (s *Service) cameraStates() map[video.CameraID]video.State {
	if s.streams == nil {
		return nil
	}
	all := s.streams.States()
	out := make(map[video.CameraID]video.State, len(all))
	for _, st := range all {
		out[st.Camera.ID] = st
	}
	return out
}
