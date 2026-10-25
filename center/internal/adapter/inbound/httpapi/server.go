// Package httpapi — входной адаптер чтения состояния и управления.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/inbound/collector"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/bcast"
	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
	"github.com/ArnurKabdylkak/lss/center/internal/app/command"
	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/app/query"
	apprescuers "github.com/ArnurKabdylkak/lss/center/internal/app/rescuers"
	approoms "github.com/ArnurKabdylkak/lss/center/internal/app/rooms"
	apptraining "github.com/ArnurKabdylkak/lss/center/internal/app/training"
	appvideo "github.com/ArnurKabdylkak/lss/center/internal/app/video"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

type Config struct {
	Addr string // адрес API, например ":8080"
	Site string // объект по умолчанию для команд

	// VideoRoot — каталог с сегментами трансляций, VideoPrefix — путь,
	// под которым он виден снаружи. Пусто — видео не раздаётся.
	VideoRoot   string
	VideoPrefix string

	// PanelRoot — каталог собранной панели. Пусто — центр отдаёт
	// только API, панель поднимается отдельно.
	PanelRoot string

	// RecordRoot — каталог видеозаписей занятий. Пусто — записи не
	// ведутся и не раздаются.
	RecordRoot string
}

type Server struct {
	srv *http.Server
	log *slog.Logger
}

func New(cfg Config, q *query.Service, cmds *command.Service,
	coll *collector.Handler, stream *bcast.SSE, users *auth.Service,
	cams *appvideo.Supervisor, rooms *approoms.Service,
	lessons *apptraining.Service, cards *apprescuers.Service, log *slog.Logger) *Server {

	h := &handler{q: q, cmds: cmds, stream: stream, cfg: cfg, log: log}
	g := guard{auth: users}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Вход оператора тренажёра.
	mux.HandleFunc("POST /api/v1/login", g.login)
	mux.HandleFunc("POST /api/v1/logout", g.logout)
	mux.HandleFunc("GET /api/v1/me", g.me)

	// Чтение — любому вошедшему.
	mux.Handle("GET /api/v1/devices", g.viewer(h.listDevices))
	mux.Handle("GET /api/v1/devices/{id}", g.viewer(h.getDevice))
	mux.Handle("GET /api/v1/devices/{id}/history", g.viewer(h.deviceHistory))
	mux.Handle("GET /api/v1/stream", g.viewer(h.stream_))
	mux.Handle("GET /api/v1/commands", g.viewer(h.listCommands))

	// Картотека спасателей. Читать вправе любой вошедший — без списка
	// не назначить участников; заводить и править — оператор.
	if cards != nil {
		rh := rescuersHandler{rescuers: cards}
		mux.Handle("GET /api/v1/rescuers", g.viewer(rh.list))
		mux.Handle("GET /api/v1/rescuers/{iin}", g.viewer(rh.get))
		mux.Handle("POST /api/v1/rescuers", g.operator(rh.create))
		mux.Handle("PUT /api/v1/rescuers/{iin}", g.operator(rh.update))
		mux.Handle("POST /api/v1/rescuers/{iin}/archive", g.operator(rh.archive))
	}

	// Комнаты объекта: раскладка вместе с текущим состоянием.
	if rooms != nil {
		rh := roomsHandler{rooms: rooms, video: videoHandler{sup: cams}, training: lessons}
		mux.Handle("GET /api/v1/rooms", g.viewer(rh.list))
		mux.Handle("GET /api/v1/rooms/{id}", g.viewer(rh.get))

		// Перевод комнаты и аварийная остановка — действия оператора.
		mux.Handle("POST /api/v1/rooms/{id}/roster", g.operator(rh.setRoster))
		mux.Handle("POST /api/v1/rooms/{id}/equipment", g.operator(rh.setUnit))
		mux.Handle("POST /api/v1/rooms/{id}/prepare", g.operator(rh.startPreparation))
		mux.Handle("POST /api/v1/rooms/{id}/state", g.operator(rh.setState))
		mux.Handle("POST /api/v1/rooms/{id}/rubble", g.operator(rh.rubble))
		mux.Handle("POST /api/v1/alarm", g.operator(rh.alarm))
		// Журнал аварий — наблюдателю: это разбор, а не управление.
		mux.Handle("GET /api/v1/alarms", g.viewer(rh.alarms))
	}

	// Занятие: открыть под ФИО и закрыть с результатом. Это действие,
	// а не наблюдение, поэтому роль «оператор».
	if lessons != nil {
		th := trainingHandler{training: lessons}
		mux.Handle("GET /api/v1/rooms/{id}/sessions", g.viewer(th.recent))
		// Журнал результатов целиком: отдельный экран смотрит все
		// занятия объекта, а не по комнате.
		mux.Handle("GET /api/v1/sessions", g.viewer(th.all))
		mux.Handle("POST /api/v1/rooms/{id}/session", g.operator(th.start))
		mux.Handle("POST /api/v1/rooms/{id}/session/finish", g.operator(th.finish))
		mux.Handle("POST /api/v1/rooms/{id}/session/assign", g.operator(th.assign))
		mux.Handle("POST /api/v1/rooms/{id}/session/target", g.operator(th.target))
		mux.Handle("POST /api/v1/rooms/{id}/session/task", g.operator(th.markTask))
		mux.Handle("POST /api/v1/sessions/{id}/comment", g.operator(th.comment))
	}

	// Видеонаблюдение. Трансляции — наблюдателю: смотреть камеры
	// вправе любой вошедший, управлять ими из панели нельзя.
	if cams != nil {
		v := videoHandler{sup: cams}
		mux.Handle("GET /api/v1/cameras", g.viewer(v.list))
		mux.Handle("GET /api/v1/cameras/{id}", g.viewer(v.get))

		if cfg.VideoRoot != "" {
			prefix := strings.TrimRight(cfg.VideoPrefix, "/")
			// Плеер запрашивает сегменты сам, заголовки к ним не
			// добавляет — токен принимается и в адресе (см. tokenOf).
			mux.Handle("GET "+prefix+"/", g.viewerHandler(hlsFiles(cfg.VideoRoot, prefix)))
		}
	}

	// Управление — роль «оператор».
	mux.Handle("POST /api/v1/commands", g.operator(h.postCommand))

	// Приём от сборщиков.
	if coll != nil {
		mux.HandleFunc("POST /api/v1/telemetry", coll.Post)
		mux.HandleFunc("POST /api/v1/acks", coll.Ack)
	}

	// Видеозаписи занятий. Смотреть вправе любой вошедший: ролик
	// приобщается к карточке, и доступ к нему тот же, что к карточке.
	if cfg.RecordRoot != "" {
		mux.Handle("GET /records/", g.viewerHandler(recordFiles(cfg.RecordRoot, "/records")))
	}

	// Панель регистрируется последней: её маршрут «/» принимает всё,
	// что не разобрали обработчики выше.
	//
	// Каталог задан не всегда: на Windows панель вшита в сам
	// исполняемый файл, и каталога рядом с ним нет.
	if panelAvailable(cfg.PanelRoot) {
		mux.Handle("GET /", panelFiles(cfg.PanelRoot))
	}

	return &Server{
		srv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		log: log,
	}
}

// Run обслуживает запросы до отмены контекста, затем гасит сервер мягко.
//
// Остановка двухступенчатая. Shutdown ждёт, пока закроются соединения,
// а поток событий панели (SSE) живёт, пока открыта вкладка: ждать его
// бессмысленно — сервер не погасится никогда и выйдет по таймауту с
// ошибкой, а перезапущенный контейнер останется лежать. Поэтому
// обычным запросам даём доиграть, а затем снимаем базовый контекст:
// по нему долгие обработчики сами выходят из цикла.
func (s *Server) Run(ctx context.Context) error {
	base, closeBase := context.WithCancel(context.WithoutCancel(ctx))
	defer closeBase()
	s.srv.BaseContext = func(net.Listener) context.Context { return base }

	errc := make(chan error, 1)
	go func() {
		s.log.Info("HTTP API слушает", "addr", s.srv.Addr)
		errc <- s.srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- s.srv.Shutdown(shutdown) }()

		select {
		case err := <-done:
			return err
		case <-time.After(gracePeriod):
			// Короткие запросы к этому моменту ответили; остались
			// только подписки на поток.
			closeBase()
			return <-done
		}
	}
}

// gracePeriod — сколько ждать обычные запросы, прежде чем обрывать
// долгие подписки. Запрос к базе укладывается в доли секунды.
const gracePeriod = time.Second

type handler struct {
	q      *query.Service
	cmds   *command.Service
	stream *bcast.SSE
	cfg    Config
	log    *slog.Logger
}

func (h *handler) listDevices(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	packets, err := h.q.Devices(r.Context())
	if err != nil {
		h.fail(w, http.StatusInternalServerError, "не удалось прочитать состояние", err)
		return
	}
	h.write(w, http.StatusOK, toDTOs(packets))
}

func (h *handler) getDevice(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	id := telemetry.DeviceID(r.PathValue("id"))

	p, ok, err := h.q.Device(r.Context(), id)
	if err != nil {
		h.fail(w, http.StatusInternalServerError, "не удалось прочитать состояние", err)
		return
	}
	if !ok {
		h.fail(w, http.StatusNotFound, "устройство неизвестно", nil)
		return
	}
	h.write(w, http.StatusOK, toDTO(p))
}

// deviceHistory отдаёт прошлое устройства: GET …/history?from=&to=&events=1
func (h *handler) deviceHistory(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	q := port.HistoryQuery{
		Device:     telemetry.DeviceID(r.PathValue("id")),
		From:       parseTime(r.URL.Query().Get("from"), time.Now().Add(-time.Hour)),
		To:         parseTime(r.URL.Query().Get("to"), time.Now()),
		EventsOnly: r.URL.Query().Get("events") == "1",
		Limit:      parseInt(r.URL.Query().Get("limit"), 1000),
	}

	packets, err := h.q.History(r.Context(), q)
	if errors.Is(err, query.ErrNoHistory) {
		h.fail(w, http.StatusServiceUnavailable, err.Error(), nil)
		return
	}
	if err != nil {
		h.fail(w, http.StatusInternalServerError, "не удалось прочитать историю", err)
		return
	}
	h.write(w, http.StatusOK, toDTOs(packets))
}

// stream_ — поток изменений состояния (SSE).
func (h *handler) stream_(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	flusher, ok := w.(http.Flusher)
	if !ok || h.stream == nil {
		h.fail(w, http.StatusServiceUnavailable, "поток недоступен", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	packets, unsubscribe := h.stream.Subscribe()
	defer unsubscribe()

	// Пустой комментарий раз в интервал не даёт прокси закрыть
	// соединение на тихом объекте.
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case p, ok := <-packets:
			if !ok {
				return
			}
			fmt.Fprint(w, "data: ")
			if err := enc.Encode(toDTO(p)); err != nil {
				return
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}

func (h *handler) listCommands(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	site := siteOr(r.URL.Query().Get("site"), h.cfg.Site)

	states, err := h.cmds.Pending(r.Context(), site)
	if err != nil {
		h.fail(w, http.StatusInternalServerError, "не удалось прочитать очередь", err)
		return
	}

	out := make([]commandDTO, 0, len(states))
	for _, st := range states {
		out = append(out, toCommandDTO(st))
	}
	h.write(w, http.StatusOK, out)
}

type commandRequest struct {
	Site string `json:"site"`
	// Имя оператора из тела используется, только когда учётные записи
	// не настроены: иначе имя берётся из сессии, а не из того, что
	// клиент о себе написал.
	Operator string  `json:"operator"`
	Device   string  `json:"device"`
	Kind     string  `json:"kind"`
	LimitC   float64 `json:"limit_c"`
	RestoreC float64 `json:"restore_c"`
	PeriodMS uint32  `json:"period_ms"`
	WheelMM  uint32  `json:"wheel_mm"`
	Obstacle uint8   `json:"obstacle"`
	Code     string  `json:"code"`
}

func (h *handler) postCommand(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req commandRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		h.fail(w, http.StatusBadRequest, "тело запроса не разобрано", nil)
		return
	}

	cmd := telemetry.Command{
		Device:   telemetry.DeviceID(req.Device),
		Kind:     telemetry.CommandKind(req.Kind),
		LimitC:   req.LimitC,
		RestoreC: req.RestoreC,
		PeriodMS: req.PeriodMS,
		WheelMM:  req.WheelMM,
		Obstacle: req.Obstacle,
		Code:     req.Code,
		Operator: operatorName(sess, req.Operator),
	}

	issued, err := h.cmds.Issue(r.Context(), siteOr(req.Site, h.cfg.Site), cmd)
	if err != nil {
		// Негодная команда — это ошибка оператора, а не сбой центра.
		h.fail(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	// Команда принята к доставке, но ещё не исполнена: плата может её
	// отклонить (§7.4). Поэтому 202, а не 200.
	h.write(w, http.StatusAccepted, map[string]string{
		"id":     issued.ID,
		"status": "pending",
	})
}

func (h *handler) write(w http.ResponseWriter, code int, body any) {
	writeJSON(w, code, body)
}

func (h *handler) fail(w http.ResponseWriter, code int, msg string, err error) {
	if err != nil {
		h.log.Error(msg, "err", err)
	}
	fail(w, code, msg)
}

func parseTime(s string, def time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return def
}

func parseInt(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func siteOr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
