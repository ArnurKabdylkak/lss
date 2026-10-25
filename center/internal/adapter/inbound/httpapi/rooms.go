package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	approoms "github.com/ArnurKabdylkak/lss/center/internal/app/rooms"
	apptraining "github.com/ArnurKabdylkak/lss/center/internal/app/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Комнаты объекта для панели: раскладка вместе с текущим состоянием.

type roomDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Capacity — сколько человек помещается в комнату: панель по
	// нему ограничивает состав команды (сценарий §4).
	Capacity int `json:"capacity,omitempty"`

	// State — состояние комнаты: ожидание, подготовка, готова,
	// тренировка, завершение, авария.
	State       string    `json:"state"`
	StateTitle  string    `json:"state_title"`
	StateSince  time.Time `json:"state_since"`
	StateReason string    `json:"state_reason,omitempty"`

	// TempC — температура воздуха с датчика пушки; HasTemp отличает
	// ноль градусов от молчащего датчика.
	TempC   float64 `json:"temp_c"`
	HasTemp bool    `json:"has_temp"`

	// Preparation — ход подготовки: цель, таймер, сообщение.
	Preparation *prepDTO `json:"preparation,omitempty"`

	// Code — код задания «Набор чисел» на эту тренировку.
	// Администратор видит его до старта и сообщает участникам.
	Code string `json:"code,omitempty"`

	// Exercises — что делают на тренажёрах сейчас: показатели каждого
	// спасателя отдельно, его время и выполнение задания.
	Exercises []exerciseDTO `json:"exercises,omitempty"`

	// Floor — этаж внутри секции, 0 — секция из одной комнаты.
	// SectionName — как называется секция целиком, SectionRooms —
	// сколько в ней помещений. По ним панель показывает дымокамеру
	// одним окном с двумя этажами, а не двумя отдельными комнатами.
	Floor        int    `json:"floor,omitempty"`
	SectionName  string `json:"section_name,omitempty"`
	SectionRooms int    `json:"section_rooms,omitempty"`

	// Rotations — сколько раз звено сменило тренажёры: круг двигает
	// центр сам, и оператор должен видеть, что это произошло.
	Rotations int `json:"rotations,omitempty"`

	// Tasks — задания маршрута и отметки о прохождении. Задание
	// проходит команда целиком.
	Tasks []taskDTO `json:"tasks,omitempty"`

	// Route — ход маршрута по всей секции: у дымокамеры он идёт
	// через две комнаты.
	Route *routeDTO `json:"route,omitempty"`

	// Faults — неисправности комнаты для главного экрана.
	Faults []faultDTO `json:"faults"`

	// Section — секция, в которую входит комната. У дымокамеры это
	// две комнаты с общим занятием.
	Section string `json:"section,omitempty"`

	// Roster — состав, записанный до подготовки: без него помещение
	// не готовят.
	Roster *rosterDTO `json:"roster,omitempty"`

	// Equipment — узлы оборудования помещения и их состояние:
	// подготовка включает их разом, оператор правит вручную.
	Equipment []unitDTO `json:"equipment,omitempty"`

	// Sensor — датчик воздуха помещения, если температуру берут не с
	// тепловой пушки. В дымокамере он свой на каждую комнату.
	Sensor string `json:"sensor,omitempty"`

	// Session — открытое занятие секции; nil, когда тренажёры
	// свободны и панель показывает поле ввода ФИО.
	Session *sessionDTO `json:"session,omitempty"`

	Devices []roomDeviceDTO   `json:"devices"`
	Heater  *roomDeviceDTO    `json:"heater,omitempty"`
	Limits  *roomHeaterLimits `json:"heater_limits,omitempty"`
	Cameras []cameraDTO       `json:"cameras"`
}

type roomDeviceDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Draft — контракт этого вида ещё не согласован с изготовителем
	// плат: панель показывает такие показания с оговоркой.
	Draft bool `json:"draft,omitempty"`

	Seen  bool       `json:"seen"`
	Stale bool       `json:"stale"`
	Last  *deviceDTO `json:"last,omitempty"`
}

// prepDTO — то, что показывает экран подготовки (сценарий §3):
// текущая и целевая температура, таймер и сообщение.
type prepDTO struct {
	TargetC     float64    `json:"target_c,omitempty"`
	Started     time.Time  `json:"started"`
	Until       *time.Time `json:"until,omitempty"`
	SecondsLeft int        `json:"seconds_left"`
	Reached     bool       `json:"reached"`
	ReachedAt   *time.Time `json:"reached_at,omitempty"`
	Expired     bool       `json:"expired"`
	Message     string     `json:"message"`
}

// exerciseDTO — упражнение на тренажёре: кто, сколько времени,
// сколько сделал и выполнено ли задание (сценарий §5).
type exerciseDTO struct {
	Device string `json:"device"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	IIN    string `json:"iin,omitempty"`
	Busy   bool   `json:"busy"`

	Seconds int `json:"seconds"`

	// Показатели — прирост с момента назначения.
	DistanceM float64 `json:"distance_m,omitempty"`
	Steps     uint16  `json:"steps,omitempty"`
	Floors    uint16  `json:"floors,omitempty"`
	Reps      uint16  `json:"reps,omitempty"`
	MaxLoadKG float64 `json:"max_load_kg,omitempty"`

	Target *targetDTO `json:"target,omitempty"`
	// SecondsLeft — сколько осталось до конца отведённого срока.
	SecondsLeft int  `json:"seconds_left,omitempty"`
	Done        bool `json:"done"`
}

// targetDTO — задание тренажёру. У велоэргометра и лестницы это срок,
// у ударного молота — подъёмы.
type targetDTO struct {
	Seconds int    `json:"seconds,omitempty"`
	Reps    uint16 `json:"reps,omitempty"`
}

// unitDTO — узел оборудования помещения.
type unitDTO struct {
	Unit string `json:"unit"`
	Name string `json:"name"`
	// On — подтверждённое состояние: сборщик отчитался, что реле
	// щёлкнуло. Лампа на пульте и значок в панели горят по нему.
	On bool `json:"on"`
	// Wait — команда ушла, ответа сборщика ещё нет. Пока он не
	// пришёл, узел не считается ни включённым, ни выключенным.
	Wait bool `json:"wait,omitempty"`
	// Want — каким узел станет, если сборщик подтвердит. Имеет смысл
	// только вместе с wait.
	Want bool `json:"want,omitempty"`
}

// routeDTO — сколько заданий маршрута пройдено по всей секции.
type routeDTO struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// rosterDTO — состав, записанный на предстоящую тренировку.
type rosterDTO struct {
	Team     string    `json:"team,omitempty"`
	Rescuers []string  `json:"rescuers"`
	Size     int       `json:"size"`
	Author   string    `json:"author,omitempty"`
	Since    time.Time `json:"since"`
}

// taskDTO — задание маршрута с отметкой (сценарий дымокамеры §1).
type taskDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
	// Auto — задание отмечается по сигналу устройства, а не человеком.
	Auto bool `json:"auto"`

	Done bool       `json:"done"`
	At   *time.Time `json:"at,omitempty"`
	By   string     `json:"by,omitempty"`
	// ByDevice — отметка пришла сигналом устройства.
	ByDevice bool `json:"by_device,omitempty"`
}

type faultDTO struct {
	Device string `json:"device,omitempty"`
	Text   string `json:"text"`
}

type roomHeaterLimits struct {
	LimitC  float64 `json:"limit_c"`
	ResumeC float64 `json:"resume_c"`
}

type roomsHandler struct {
	rooms    *approoms.Service
	video    videoHandler
	training *apptraining.Service
}

func (h roomsHandler) list(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	views, err := h.rooms.Rooms(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "не удалось прочитать раскладку объекта")
		return
	}

	out := make([]roomDTO, 0, len(views))
	for _, v := range views {
		out = append(out, h.withSession(r.Context(), h.toDTO(v)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h roomsHandler) get(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	view, ok, err := h.rooms.Room(r.Context(), layout.RoomID(r.PathValue("id")))
	if err != nil {
		fail(w, http.StatusInternalServerError, "не удалось прочитать раскладку объекта")
		return
	}
	if !ok {
		fail(w, http.StatusNotFound, "комната неизвестна")
		return
	}
	writeJSON(w, http.StatusOK, h.withSession(r.Context(), h.toDTO(view)))
}

// withSession дополняет комнату открытым занятием.
func (h roomsHandler) withSession(ctx context.Context, dto roomDTO) roomDTO {
	if h.training == nil {
		return dto
	}
	sess, ok, err := h.training.Active(ctx, layout.RoomID(dto.ID))
	if err != nil || !ok {
		return dto
	}
	s := toSessionDTO(sess)
	dto.Session = &s
	return dto
}

func (h roomsHandler) toDTO(v approoms.RoomView) roomDTO {
	dto := roomDTO{
		ID:           string(v.Room.ID),
		Section:      v.Section,
		SectionName:  v.SectionName,
		SectionRooms: v.SectionRooms,
		Floor:        v.Room.Floor,
		Name:         v.Room.Title(),
		Capacity:     v.Room.Limit(),
		State:        string(v.State.State),
		StateTitle:   v.State.State.Title(),
		StateSince:   v.State.Since,
		StateReason:  v.State.Reason,
		Code:         v.State.Code,
		Roster:       roster(v.State.Roster),
		Sensor:       v.Room.Sensor,
		Equipment:    units(v),
		TempC:        v.TempC,
		HasTemp:      v.HasTemp,
		Devices:      make([]roomDeviceDTO, 0, len(v.Devices)),
		Cameras:      make([]cameraDTO, 0, len(v.Cameras)),
	}

	for _, d := range v.Devices {
		dto.Devices = append(dto.Devices, deviceView(d))
	}
	if v.Heater != nil {
		hv := deviceView(*v.Heater)
		dto.Heater = &hv
		dto.Limits = &roomHeaterLimits{
			LimitC:  v.Room.Heater.LimitC,
			ResumeC: v.Room.Heater.ResumeC,
		}
	}
	if p := v.State.Prep; p.Running() {
		now := time.Now()
		prep := &prepDTO{
			TargetC:     p.TargetC,
			Started:     p.Started,
			SecondsLeft: int(p.Left(now).Seconds()),
			Reached:     p.Done(),
			Expired:     p.Expired(now),
			Message:     p.Message(now),
		}
		if !p.Until.IsZero() {
			until := p.Until
			prep.Until = &until
		}
		if p.Done() {
			reached := p.Reached
			prep.ReachedAt = &reached
		}
		dto.Preparation = prep
	}

	dto.Rotations = v.Rotations

	for _, e := range v.Exercises {
		item := exerciseDTO{
			Device:    e.Device.ID,
			Name:      e.Device.Title(),
			Kind:      string(e.Device.Kind),
			IIN:       e.IIN,
			Busy:      e.Busy(),
			Seconds:   int(e.Elapsed.Seconds()),
			DistanceM: e.Progress.DistanceM,
			Steps:     e.Progress.Steps,
			Floors:    e.Progress.Floors,
			// Подъёмы приходят от платы: это её счётчик, а не
			// число подходов, посчитанное центром.
			// Выполнено подъёмов: считается от показаний платы, а не
			// от того, что задал оператор.
			Reps:      e.Progress.Sets,
			MaxLoadKG: e.Progress.MaxLoadKG,
			Done:      e.Done,
		}
		if e.Target.Set() {
			item.Target = &targetDTO{
				Seconds: int(e.Target.Duration.Seconds()),
				Reps:    e.Target.Reps,
			}
			item.SecondsLeft = int(e.Left.Seconds())
		}
		dto.Exercises = append(dto.Exercises, item)
	}

	for _, t := range v.Tasks {
		item := taskDTO{
			ID: t.Task.ID, Name: t.Task.Name, Note: t.Task.Note,
			Auto: t.Task.Auto(), Done: t.Done, By: t.By, ByDevice: t.Auto,
		}
		if t.Done && !t.At.IsZero() {
			at := t.At
			item.At = &at
		}
		dto.Tasks = append(dto.Tasks, item)
	}
	if v.Route.Total > 0 {
		dto.Route = &routeDTO{Done: v.Route.Done, Total: v.Route.Total}
	}

	dto.Faults = make([]faultDTO, 0, len(v.Faults))
	for _, f := range v.Faults {
		dto.Faults = append(dto.Faults, faultDTO{Device: f.Device, Text: f.Text})
	}

	for _, c := range v.Cameras {
		cam := h.video.toDTO(c.State)
		cam.Playlist = c.Playlist
		dto.Cameras = append(dto.Cameras, cam)
	}

	return dto
}

func deviceView(d approoms.DeviceView) roomDeviceDTO {
	out := roomDeviceDTO{
		ID:    d.Device.ID,
		Name:  d.Device.Title(),
		Kind:  string(d.Device.Kind),
		Draft: d.Device.Kind.Draft(),
		Seen:  d.Seen,
		Stale: d.Stale,
	}
	if d.Seen {
		last := toDTO(d.Last)
		out.Last = &last
	}
	return out
}

type stateRequest struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// setState переводит комнату: POST /api/v1/rooms/{id}/state
func (h roomsHandler) setState(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req stateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	// Состояние меняется у секции целиком: у дымокамеры два этажа —
	// одно занятие, и закрывать смену по этажам отдельно значило бы
	// оставить половину зала в прежнем состоянии.
	st, err := h.rooms.SetStateSection(r.Context(), layout.RoomID(r.PathValue("id")),
		layout.State(req.State), req.Reason, operatorName(sess, ""))
	switch {
	case errors.Is(err, layout.ErrBadTransition):
		// 409: переход не предусмотрен порядком работы — например,
		// тренировка из ожидания, минуя подготовку.
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stateDTO(st))
}

// alarm объявляет аварийную остановку: POST /api/v1/alarm
//
// Это дублирование аппаратной петли: нагрузку снимает реле
// безопасности, а центр останавливает учёт и сохраняет записи.
func (h roomsHandler) alarm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req stateRequest
	// Тело необязательно: кнопку жмут в спешке.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)

	states, err := h.rooms.Alarm(r.Context(), req.Reason, operatorName(sess, ""))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]roomStateDTO, 0, len(states))
	for _, st := range states {
		out = append(out, stateDTO(st))
	}
	writeJSON(w, http.StatusOK, out)
}

type roomStateDTO struct {
	Room   string    `json:"room"`
	State  string    `json:"state"`
	Title  string    `json:"title"`
	Since  time.Time `json:"since"`
	Reason string    `json:"reason,omitempty"`
	Author string    `json:"author,omitempty"`
}

func stateDTO(s layout.RoomState) roomStateDTO {
	return roomStateDTO{
		Room:   string(s.Room),
		State:  string(s.State),
		Title:  s.State.Title(),
		Since:  s.Since,
		Reason: s.Reason,
		Author: s.Author,
	}
}

// startPreparation запускает подготовку комнаты:
// POST /api/v1/rooms/{id}/prepare
func (h roomsHandler) startPreparation(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	// Подготовка идёт по секции: звено заходит в дымокамеру целиком,
	// и греть, дымить и светить этажи начинают разом.
	st, err := h.rooms.StartPreparationSection(r.Context(),
		layout.RoomID(r.PathValue("id")), operatorName(sess, ""))
	switch {
	// Оба случая — отказ по порядку работы, а не негодный запрос:
	// панель показывает причину дословно.
	case errors.Is(err, layout.ErrBadTransition), errors.Is(err, approoms.ErrNoRoster):
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stateDTO(st))
}

type alarmDTO struct {
	ID       string     `json:"id"`
	At       time.Time  `json:"at"`
	Reason   string     `json:"reason,omitempty"`
	Author   string     `json:"author,omitempty"`
	Rooms    []string   `json:"rooms"`
	Sessions []string   `json:"sessions,omitempty"`
	Reset    *time.Time `json:"reset,omitempty"`
	Active   bool       `json:"active"`
}

// alarms отдаёт журнал аварийных остановок:
// GET /api/v1/alarms?limit=
func (h roomsHandler) alarms(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	events, err := h.rooms.Alarms(r.Context(), parseInt(r.URL.Query().Get("limit"), 50))
	if err != nil {
		fail(w, http.StatusInternalServerError, "не удалось прочитать журнал аварий")
		return
	}

	out := make([]alarmDTO, 0, len(events))
	for _, e := range events {
		item := alarmDTO{
			ID: e.ID, At: e.At, Reason: e.Reason, Author: e.Author,
			Sessions: e.Sessions, Active: e.Active(),
		}
		for _, room := range e.Rooms {
			item.Rooms = append(item.Rooms, string(room))
		}
		if !e.Reset.IsZero() {
			reset := e.Reset
			item.Reset = &reset
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// rubble отпускает трубы механизма «Завалы»:
// POST /api/v1/rooms/{id}/rubble
func (h roomsHandler) rubble(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	err := h.rooms.Rubble(r.Context(), layout.RoomID(r.PathValue("id")), operatorName(sess, ""))
	switch {
	case errors.Is(err, layout.ErrBadTransition):
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "завалы включены"})
}

// roster переводит состав в вид для панели. Пустой состав не
// показывается вовсе: «записано ноль человек» и «состав не записан» —
// разные вещи, и первое вводило бы в заблуждение.
func roster(r layout.Roster) *rosterDTO {
	if !r.Set() {
		return nil
	}
	return &rosterDTO{
		Team:     r.Team,
		Rescuers: r.Rescuers,
		Size:     r.Size(),
		Author:   r.Author,
		Since:    r.Since,
	}
}

// setRoster записывает состав: POST /api/v1/rooms/{id}/roster
//
// ФИО и ИИН заносят до подготовки помещения — без состава прогрев не
// начинают.
func (h roomsHandler) setRoster(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req struct {
		Team     string   `json:"team"`
		Rescuers []string `json:"rescuers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	// Состав записывается на секцию целиком: у дымокамеры занятие
	// одно на два этажа, и вводить одних и тех же людей дважды
	// незачем.
	st, err := h.rooms.SetRosterSection(r.Context(), layout.RoomID(r.PathValue("id")),
		req.Team, req.Rescuers, operatorName(sess, ""))

	switch {
	case errors.Is(err, training.ErrCapacity), errors.Is(err, layout.ErrBadTransition):
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "состав записан",
		"roster": roster(st.Roster),
	})
}

// units перечисляет узлы помещения с их состоянием.
func units(v approoms.RoomView) []unitDTO {
	list := v.Room.Units(v.Section)
	out := make([]unitDTO, 0, len(list))
	for _, u := range list {
		want, wait := v.State.Pending[u.ID]
		out = append(out, unitDTO{
			Unit: u.ID,
			Name: u.Name,
			On:   v.State.Equipment[u.ID],
			Wait: wait,
			Want: want,
		})
	}
	return out
}

// setUnit включает или выключает узел: POST /api/v1/rooms/{id}/equipment
func (h roomsHandler) setUnit(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req struct {
		Unit string `json:"unit"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	st, err := h.rooms.SetUnit(r.Context(), layout.RoomID(r.PathValue("id")),
		req.Unit, req.On, operatorName(sess, ""))

	switch {
	case errors.Is(err, layout.ErrBadTransition):
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"unit": req.Unit,
		"on":   st.Equipment[req.Unit],
	})
}
