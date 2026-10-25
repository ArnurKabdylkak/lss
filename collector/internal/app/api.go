package app

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Локальная панель объекта. Работает без связи с центром — в этом смысл
// промежуточного звена: оператор на объекте видит оборудование, даже
// когда канал наверх лежит.
type API struct {
	state   *State
	uplink  *Uplink
	buf     *DiskBuffer
	send    CommandSender
	outputs *Outputs
	room    *Room
	cfg     Config
	log     *slog.Logger
}

func NewAPI(cfg Config, state *State, uplink *Uplink, buf *DiskBuffer,
	send CommandSender, outputs *Outputs, room *Room, log *slog.Logger) *API {

	return &API{state: state, uplink: uplink, buf: buf, send: send,
		outputs: outputs, room: room, cfg: cfg, log: log}
}

// Routes собирает маршруты локального API.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/devices", a.devices)
	mux.HandleFunc("GET /api/v1/devices/{id}", a.device)
	mux.HandleFunc("GET /api/v1/status", a.status)

	// Аварийная остановка с пульта, минуя центр.
	//
	// Обычные команды приходят только в ответе центра на выгрузку:
	// входящих соединений сборщик для них не принимает (§7.2). Авария
	// — исключение, и сделано оно осознанно: остановка зала не должна
	// зависеть от того, жив ли центр. Путь защищён общим секретом,
	// потому что в обход центра проверять права некому.
	mux.HandleFunc("POST /alarm", a.alarm)

	return logging(a.log, recovering(a.log, mux))
}

func (a *API) devices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.state.Snapshot())
}

func (a *API) device(w http.ResponseWriter, r *http.Request) {
	st, ok := a.state.Device(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "устройство неизвестно"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// status — состояние самого сборщика: глубина буфера и связь с центром.
// Это первое, что спрашивают при разборе «почему в центре нет данных».
func (a *API) status(w http.ResponseWriter, _ *http.Request) {
	out := struct {
		Site    string        `json:"site"`
		Devices int           `json:"devices"`
		Uplink  Status        `json:"uplink"`
		Outputs []HeaterState `json:"outputs,omitempty"`
	}{
		Site:    a.cfg.Site,
		Devices: len(a.state.Snapshot()),
		Uplink:  a.uplink.Status(),

		// Состояние реле видно на объекте, даже когда канал наверх
		// лежит: там, где стоит железо, оно нужнее всего.
		Outputs: a.outputs.States(),
	}

	writeJSON(w, http.StatusOK, out)
}

// alarm — POST /alarm: остановить помещение немедленно.
func (a *API) alarm(w http.ResponseWriter, r *http.Request) {
	if a.cfg.AlarmToken == "" {
		writeJSON(w, http.StatusNotFound,
			map[string]string{"error": "прямая авария на этом сборщике не включена"})
		return
	}
	if !tokenOK(r.Header.Get("Authorization"), a.cfg.AlarmToken) {
		writeJSON(w, http.StatusUnauthorized,
			map[string]string{"error": "неверный секрет"})
		return
	}
	if a.room == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "помещение на сборщике не настроено"})
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	// Тело необязательно: причина — для журнала, а не для решения.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req)
	if req.Reason == "" {
		req.Reason = "аварийная остановка с пульта"
	}

	a.log.Error("АВАРИЙНАЯ ОСТАНОВКА с пульта, минуя центр", "причина", req.Reason)
	if err := a.room.Alarm(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "помещение остановлено"})
}

// tokenOK сравнивает секрет за постоянное время: иначе по времени
// ответа секрет подбирается побайтно.
func tokenOK(header, want string) bool {
	got := strings.TrimSpace(strings.TrimPrefix(header, "Bearer"))
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// Server поднимает локальное API.
type Server struct {
	srv *http.Server
	log *slog.Logger
}

func NewServer(addr string, h http.Handler, log *slog.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
		},
		log: log,
	}
}
