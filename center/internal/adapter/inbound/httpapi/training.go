package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	apptraining "github.com/ArnurKabdylkak/lss/center/internal/app/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Занятие: кто сейчас на тренажёрах комнаты.
//
// Открывает и закрывает занятие оператор — это действие, а не
// наблюдение, поэтому наблюдателю оно недоступно.

type sessionDTO struct {
	ID   string `json:"id"`
	Room string `json:"room"`
	// Section и Rooms — секция и её комнаты: у дымокамеры маршрут
	// идёт через две комнаты с общим занятием.
	Section  string   `json:"section,omitempty"`
	Rooms    []string `json:"rooms,omitempty"`
	Examinee string   `json:"examinee"`
	// Team — название команды и её состав.
	Team     string     `json:"team,omitempty"`
	Size     int        `json:"size"`
	Rescuers []string   `json:"rescuers,omitempty"`
	Operator string     `json:"operator,omitempty"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	Active   bool       `json:"active"`
	// Code и Attempts — задание «Набор чисел»: код этой тренировки и
	// сколько раз его вводили.
	Code     string `json:"code,omitempty"`
	Attempts int    `json:"attempts,omitempty"`

	// Reason — причина досрочного завершения, если оно было.
	Reason  string     `json:"reason,omitempty"`
	Seconds int        `json:"seconds"` // длительность занятия
	Result  *resultDTO `json:"result,omitempty"`

	// Assignments — кто какой тренажёр занимает сейчас и занимал.
	Assignments []assignmentDTO `json:"assignments,omitempty"`

	// Records — видеозаписи занятия, по одной на камеру.
	Records []recordDTO `json:"records,omitempty"`

	// Tasks — отметки о прохождении заданий маршрута: когда и каким
	// способом подтверждено (сценарий §6).
	Tasks []taskMarkDTO `json:"tasks,omitempty"`

	// Report — папка отчёта и ссылка на PDF: отчёт отдают целиком,
	// поэтому папку видно так же, как и файл.
	Report    string `json:"report,omitempty"`
	ReportURL string `json:"report_url,omitempty"`

	// Outcome и Status — итог занятия кодом и словами.
	Outcome string `json:"outcome,omitempty"`
	Status  string `json:"status"`

	// Temp — температура помещения за время занятия.
	Temp *tempDTO `json:"temp,omitempty"`
	// Comment — комментарий администратора.
	Comment string `json:"comment,omitempty"`
}

// taskMarkDTO — отметка о прохождении задания в записи занятия.
//
// Seconds — время прохождения от старта тренировки: по сценарию в
// карточку идёт именно оно, а не абсолютный час отметки.
type taskMarkDTO struct {
	Task    string    `json:"task"`
	Done    bool      `json:"done"`
	At      time.Time `json:"at"`
	Seconds int       `json:"seconds"`
	By      string    `json:"by,omitempty"`
	Auto    bool      `json:"auto"`
}

// tempDTO — в каких условиях шло занятие (сценарий §6).
type tempDTO struct {
	MinC    float64 `json:"min_c"`
	MaxC    float64 `json:"max_c"`
	AvgC    float64 `json:"avg_c"`
	Samples int     `json:"samples"`
}

// recordDTO — ролик занятия: ссылка для просмотра и то, получился ли он.
type recordDTO struct {
	Camera  string `json:"camera"`
	URL     string `json:"url,omitempty"`
	Seconds int    `json:"seconds"`
	Bytes   int64  `json:"bytes"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// assignmentDTO — упражнение одного спасателя на одном тренажёре:
// время и результат считаются отдельно для каждого.
// assignmentDTO — кто на каком тренажёре работал и с каким итогом.
//
// Задание и выполнение идут рядом: подъёмы задаёт оператор, считает
// их плата, и в отчёте их сверяют.
type assignmentDTO struct {
	Device  string     `json:"device"`
	IIN     string     `json:"iin"`
	Since   time.Time  `json:"since"`
	Until   *time.Time `json:"until,omitempty"`
	Running bool       `json:"running"`
	Seconds int        `json:"seconds"`
	Result  *resultDTO `json:"result,omitempty"`

	// Target — что было задано на этот тренажёр.
	Target *targetDTO `json:"target,omitempty"`
	// Lifts — подъёмов выполнено по показаниям платы, LiftsTarget —
	// сколько было задано. Ноль в задании означает, что подъёмы не
	// задавались: зачёт шёл по времени.
	Lifts       uint16 `json:"lifts,omitempty"`
	LiftsTarget uint16 `json:"lifts_target,omitempty"`
}

// resultDTO — что экзаменуемый сделал за занятие: разница показаний
// между открытием и закрытием.
type resultDTO struct {
	DistanceM float64 `json:"distance_m"`
	Floors    uint16  `json:"floors"`
	Steps     uint16  `json:"steps"`
	Sets      uint16  `json:"sets"`
	MaxLoadKG float64 `json:"max_load_kg"`
}

func toSessionDTO(s training.Session) sessionDTO {
	dto := sessionDTO{
		ID:       string(s.ID),
		Room:     s.Room,
		Section:  s.Section,
		Rooms:    s.Rooms,
		Examinee: string(s.Examinee),
		Team:     s.Team,
		Size:     s.Size(),
		Rescuers: s.Rescuers,
		Operator: s.Operator,
		Code:     s.Code,
		Attempts: s.Attempts,
		Started:  s.Started,
		Active:   s.Active(),
		Reason:   s.Reason,
		Seconds:  int(s.Duration().Seconds()),
		Outcome:  string(s.Outcome),
		Status:   s.Status(),
		Report:   s.Report,
	}

	// Отметки заданий — по времени прохождения: карточку читают как
	// ход тренировки, а не как словарь.
	for id, m := range s.Tasks {
		dto.Tasks = append(dto.Tasks, taskMarkDTO{
			Task:    id,
			Done:    m.Done,
			At:      m.At,
			Seconds: int(s.TaskElapsed(id).Seconds()),
			By:      m.By,
			Auto:    m.Auto,
		})
	}
	sort.Slice(dto.Tasks, func(i, j int) bool {
		if dto.Tasks[i].At.Equal(dto.Tasks[j].At) {
			return dto.Tasks[i].Task < dto.Tasks[j].Task
		}
		return dto.Tasks[i].At.Before(dto.Tasks[j].At)
	})
	for _, a := range s.Assignments {
		item := assignmentDTO{
			Device:  a.Device,
			IIN:     a.IIN,
			Since:   a.Since,
			Running: a.Running(),
			Seconds: int(a.Duration().Seconds()),
		}
		if t := s.Target(a.Device); t.Set() {
			item.Target = &targetDTO{Seconds: int(t.Duration.Seconds()), Reps: t.Reps}
			item.LiftsTarget = t.Reps
		}
		if !a.Running() {
			until := a.Until
			item.Until = &until
			r := a.Result()
			item.Result = &resultDTO{
				DistanceM: r.DistanceM, Floors: r.Floors, Steps: r.Steps,
				Sets: r.Sets, MaxLoadKG: r.MaxLoadKG,
			}
			item.Lifts = r.Sets
		}
		dto.Assignments = append(dto.Assignments, item)
	}

	if s.Report != "" {
		dto.ReportURL = "/records/" + s.Report + "/report.pdf"
	}

	dto.Comment = s.Comment
	if s.Temp.Known() {
		dto.Temp = &tempDTO{
			MinC: s.Temp.MinC, MaxC: s.Temp.MaxC,
			AvgC: s.Temp.AvgC, Samples: s.Temp.Samples,
		}
	}

	for _, rec := range s.Records {
		item := recordDTO{
			Camera:  rec.Camera,
			Seconds: int(rec.Duration().Seconds()),
			Bytes:   rec.Bytes,
			OK:      rec.OK(),
			Error:   rec.Error,
		}
		if rec.OK() {
			item.URL = "/records/" + rec.File
		}
		dto.Records = append(dto.Records, item)
	}

	if !s.Active() {
		finished := s.Finished
		dto.Finished = &finished

		r := s.Result()
		dto.Result = &resultDTO{
			DistanceM: r.DistanceM,
			Floors:    r.Floors,
			Steps:     r.Steps,
			Sets:      r.Sets,
			MaxLoadKG: r.MaxLoadKG,
		}
	}
	return dto
}

type trainingHandler struct {
	training *apptraining.Service
}

type startRequest struct {
	// Team — название команды, проходящей тренировку.
	Team     string `json:"team"`
	Examinee string `json:"examinee"`
	// Rescuers — ИИН карточек участников: занятие может идти звеном,
	// и его результат попадёт в карточку каждого.
	Rescuers []string `json:"rescuers"`
	// Assignments — кто какой тренажёр занимает. Показания датчика
	// закрепляются за назначенной карточкой (сценарий §4).
	Assignments []assignRequest `json:"assignments"`
}

type assignRequest struct {
	Device string `json:"device"`
	// IIN пустой снимает спасателя с тренажёра: упражнение
	// закрывается, результат остаётся за ним.
	IIN string `json:"iin"`
}

// start открывает занятие: POST /api/v1/rooms/{id}/session
func (h trainingHandler) start(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req startRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	room := layout.RoomID(r.PathValue("id"))
	assignments := make([]apptraining.Assignment, 0, len(req.Assignments))
	for _, a := range req.Assignments {
		assignments = append(assignments, apptraining.Assignment{Device: a.Device, IIN: a.IIN})
	}

	out, err := h.training.Start(r.Context(), room, apptraining.StartOptions{
		Team:        req.Team,
		Examinee:    req.Examinee,
		Rescuers:    req.Rescuers,
		Assignments: assignments,
	}, operatorName(sess, ""))

	switch {
	case errors.Is(err, training.ErrNoExaminee), errors.Is(err, training.ErrBadExaminee):
		// Оператор ошибся в поле — это 400, и причину нужно показать
		// дословно: он исправит её сам.
		fail(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, rescuer.ErrNotFound), errors.Is(err, rescuer.ErrArchived):
		// Назначили карточку, которой нет в картотеке или которая
		// выведена из обращения: это ошибка ввода, а не сбой центра.
		fail(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, training.ErrCapacity), errors.Is(err, training.ErrNotAssigned):
		// Больше людей, чем помещается, или негодное назначение —
		// это ошибка ввода, её исправляет администратор.
		fail(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, training.ErrBusy):
		// 409: занятие уже идёт, и прежде чем начать новое, прошлое
		// надо закрыть — иначе показания зачтутся двоим.
		fail(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toSessionDTO(out))
}

type finishRequest struct {
	// Reason — причина досрочного завершения, если оно было.
	Reason string `json:"reason"`
	// Comment — комментарий администратора к занятию.
	Comment string `json:"comment"`
}

// finish закрывает занятие: POST /api/v1/rooms/{id}/session/finish
func (h trainingHandler) finish(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req finishRequest
	// Тело необязательно: обычное завершение идёт без причины.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)

	out, err := h.training.FinishWithReason(r.Context(),
		layout.RoomID(r.PathValue("id")), req.Reason)

	switch {
	case errors.Is(err, training.ErrNoSession):
		fail(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.Comment != "" {
		if commented, err := h.training.Comment(r.Context(), string(out.ID),
			req.Comment, operatorName(sess, "")); err == nil {
			out = commented
		}
	}

	writeJSON(w, http.StatusOK, toSessionDTO(out))
}

type commentRequest struct {
	Comment string `json:"comment"`
}

// comment пишет комментарий к занятию:
// POST /api/v1/sessions/{id}/comment
//
// Отдельно от завершения: разбор бывает не сразу, и комментарий
// дописывают к прошедшему занятию.
func (h trainingHandler) comment(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req commentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	out, err := h.training.Comment(r.Context(), r.PathValue("id"),
		req.Comment, operatorName(sess, ""))
	switch {
	case errors.Is(err, training.ErrNoSession):
		fail(w, http.StatusNotFound, "занятие не найдено")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toSessionDTO(out))
}

// all отдаёт журнал результатов объекта: GET /api/v1/sessions?limit=50
func (h *trainingHandler) all(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 500)
		}
	}

	list, err := h.training.All(r.Context(), limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]sessionDTO, 0, len(list))
	for _, s := range list {
		out = append(out, toSessionDTO(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// recent отдаёт прошлые занятия комнаты: GET /api/v1/rooms/{id}/sessions
func (h trainingHandler) recent(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	list, err := h.training.Recent(r.Context(), layout.RoomID(r.PathValue("id")))
	if err != nil {
		fail(w, http.StatusInternalServerError, "не удалось прочитать журнал занятий")
		return
	}

	out := make([]sessionDTO, 0, len(list))
	for _, s := range list {
		out = append(out, toSessionDTO(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// assign ставит или снимает спасателя с тренажёра:
// POST /api/v1/rooms/{id}/session/assign
func (h trainingHandler) assign(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var req assignRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	out, err := h.training.Assign(r.Context(), layout.RoomID(r.PathValue("id")),
		req.Device, req.IIN)
	switch {
	case errors.Is(err, training.ErrNoSession):
		fail(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, training.ErrNotAssigned):
		fail(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toSessionDTO(out))
}

type targetRequest struct {
	Device string `json:"device"`
	// Seconds — отведённое время упражнения: так зачитываются
	// велоэргометр и лестница.
	Seconds int `json:"seconds"`
	// Reps — подъёмы груза; только для ударного молота.
	Reps uint16 `json:"reps"`
}

// target задаёт задание тренажёру:
// POST /api/v1/rooms/{id}/session/target
//
// Пустое задание снимает цель: упражнение идёт без зачёта.
func (h trainingHandler) target(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var req targetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	out, err := h.training.SetTarget(r.Context(), layout.RoomID(r.PathValue("id")), req.Device,
		training.Target{
			Duration: time.Duration(req.Seconds) * time.Second,
			Reps:     req.Reps,
		})
	switch {
	case errors.Is(err, training.ErrNoSession):
		fail(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, training.ErrNotAssigned):
		fail(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toSessionDTO(out))
}

type taskRequest struct {
	Task string `json:"task"`
	Done bool   `json:"done"`
}

// markTask отмечает прохождение задания вручную:
// POST /api/v1/rooms/{id}/session/task
func (h trainingHandler) markTask(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req taskRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	out, err := h.training.MarkTask(r.Context(), layout.RoomID(r.PathValue("id")),
		req.Task, req.Done, operatorName(sess, ""))
	switch {
	case errors.Is(err, training.ErrNoSession):
		fail(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, training.ErrNotAssigned):
		fail(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toSessionDTO(out))
}
