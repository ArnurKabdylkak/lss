// Package collector — входной адаптер приёма батчей от сборщиков (§2.2).
//
// Это основной вход центра в трёхзвенной схеме. В ответ на выгрузку
// сборщик получает команды своего объекта: входящих соединений он не
// принимает, и другого способа их доставить нет (§7.2).
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/command"
	"github.com/ArnurKabdylkak/lss/center/internal/app/ingest"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// MaxBodyBytes ограничивает тело запроса: сборщик после долгого разрыва
// может попытаться выгрузить всё накопленное разом.
const MaxBodyBytes = 8 << 20

// SeenTTL — сколько помнить принятые батчи для отсева повторов.
// Повтор приходит в пределах нескольких попыток подряд, так что часов
// хватает с запасом; вечная память здесь только течёт.
const SeenTTL = 6 * time.Hour

// Owner сообщает, к какому сборщику подключено устройство.
//
// На объекте сборщиков несколько, по одному на помещение, а очередь
// команд общая для объекта. Без этого отбора первый опросивший
// сборщик забрал бы чужие команды, и у соседа они бы просто пропали.
type Owner func(ctx context.Context, device string) (collector string, known bool)

// Handler принимает выгрузки.
type Handler struct {
	ingest   *ingest.Service
	commands *command.Service
	owner    Owner
	log      *slog.Logger

	mu   sync.Mutex
	seen map[string]time.Time // BatchID -> когда принят
}

func New(ing *ingest.Service, cmds *command.Service, log *slog.Logger) *Handler {
	return &Handler{
		ingest:   ing,
		commands: cmds,
		log:      log,
		seen:     make(map[string]time.Time),
	}
}

// WithOwner подключает раскладку: кто чьим оборудованием заведует.
// Без неё центр работает как прежде — отдаёт команды тому, кто
// спросил первым.
func (h *Handler) WithOwner(o Owner) { h.owner = o }

// Post обслуживает POST /api/v1/telemetry.
func (h *Handler) Post(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	var batch telemetry.Batch
	if err := json.NewDecoder(body).Decode(&batch); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.fail(w, http.StatusRequestEntityTooLarge, "батч слишком велик")
			return
		}
		h.fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	// Батч проверяется целиком до приёма: если принять половину записей
	// и отказать, сборщик переотправит батч и принятое задвоится.
	if err := batch.Validate(); err != nil {
		h.log.Warn("батч отвергнут", "site", batch.SiteID, "batch", batch.BatchID, "err", err)
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}

	// Опрос команд не запоминается: идентификатор нужен только для
	// отсева повторной доставки данных, а пустых запросов сборщик
	// делает много.
	if batch.IsPoll() {
		h.reply(r.Context(), w, batch, telemetry.BatchResult{})
		return
	}

	if h.alreadySeen(batch.BatchID) {
		// Повтор после неподтверждённой отправки — штатная ситуация.
		// Отвечаем успехом, чтобы сборщик очистил буфер, но ничего
		// не принимаем повторно.
		h.log.Info("повтор батча отброшен", "site", batch.SiteID, "batch", batch.BatchID)
		h.reply(r.Context(), w, batch, telemetry.BatchResult{Duplicate: true})
		return
	}

	ctx := r.Context()
	for _, rec := range batch.Packets {
		h.ingest.Handle(ctx, packetOf(rec))
	}
	h.remember(batch.BatchID)

	h.log.Debug("батч принят", "site", batch.SiteID, "batch", batch.BatchID,
		"packets", len(batch.Packets))

	h.reply(ctx, w, batch, telemetry.BatchResult{Accepted: len(batch.Packets)})
}

// Ack обслуживает POST /api/v1/acks — подтверждения команд, собранные
// сборщиком с плат.
func (h *Handler) Ack(w http.ResponseWriter, r *http.Request) {
	var acks []telemetry.Ack
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&acks); err != nil {
		h.fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	ctx := r.Context()
	for _, a := range acks {
		if err := h.commands.Settle(ctx, a); err != nil {
			h.log.Error("учёт подтверждения", "id", a.ID, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// reply отдаёт результат вместе с командами, адресованными этому
// сборщику.
func (h *Handler) reply(ctx context.Context, w http.ResponseWriter,
	batch telemetry.Batch, res telemetry.BatchResult) {

	site := batch.SiteID
	if h.commands != nil && site != "" {
		cmds, err := h.commands.Take(ctx, site, h.allow(ctx, batch.CollectorID))
		if err != nil {
			h.log.Error("выдача команд", "site", site, "err", err)
		} else {
			res.Commands = cmds
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// allow строит отбор команд для этого сборщика.
func (h *Handler) allow(ctx context.Context, collector string) func(telemetry.Command) bool {
	// Сборщик не представился или раскладка не подключена — объект с
	// одним сборщиком, отдаём всё.
	if collector == "" || h.owner == nil {
		return nil
	}

	return func(cmd telemetry.Command) bool {
		device := string(cmd.Device)
		if device == collector {
			// Команда самому сборщику: подготовка, авария, узлы.
			return true
		}

		owner, known := h.owner(ctx, device)
		if !known {
			// Устройства нет в раскладке: отдавать его команду
			// наугад нельзя — исполнит не тот сборщик.
			h.log.Warn("команда устройству вне раскладки объекта",
				"device", device, "kind", cmd.Kind, "collector", collector)
			return false
		}
		return owner == collector
	}
}

func (h *Handler) alreadySeen(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.seen[id]
	return ok
}

func (h *Handler) remember(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	h.seen[id] = now
	for k, at := range h.seen {
		if now.Sub(at) > SeenTTL {
			delete(h.seen, k)
		}
	}
}

func (h *Handler) fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, telemetry.BatchResult{Error: msg})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// packetOf переводит запись батча в доменный пакет. Идентичность уже
// проставлена сборщиком, и центр её не переопределяет.
func packetOf(r telemetry.Record) telemetry.Packet {
	p := telemetry.Packet{
		Device:   telemetry.DeviceID(r.DeviceID),
		Received: r.Received,
		Data:     r.Data,
	}
	if addr, err := netip.ParseAddrPort(r.Source); err == nil {
		p.From = addr
	}
	return p
}
