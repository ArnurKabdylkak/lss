package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Uplink выгружает накопленное в центр и забирает оттуда команды.
//
// Записи удаляются из буфера ТОЛЬКО после подтверждения центром: иначе
// разрыв в момент выгрузки означал бы потерю данных, ради сохранения
// которых буфер и заведён.
type Uplink struct {
	cfg      Config
	buf      *DiskBuffer
	dispatch CommandDispatcher
	log      *slog.Logger
	client   *http.Client

	mu       sync.Mutex
	pending  []telemetry.Ack // подтверждения, ожидающие отправки наверх
	lastErr  error
	lastOK   time.Time
	attempts int
}

// CommandSender отправляет команду на плату. Реализуется приёмником:
// у него открыт сокет объекта.
type CommandSender interface {
	Send(cmd telemetry.Command) error
}

// CommandDispatcher решает, кому адресована команда: сборщику (пушка)
// или плате (препятствия, настройки измерений).
type CommandDispatcher interface {
	Dispatch(cmd telemetry.Command) (*telemetry.Ack, error)
}

func NewUplink(cfg Config, buf *DiskBuffer, dispatch CommandDispatcher, log *slog.Logger) *Uplink {
	return &Uplink{
		cfg:      cfg,
		buf:      buf,
		dispatch: dispatch,
		log:      log,
		client:   &http.Client{Timeout: cfg.Center.Timeout},
	}
}

// QueueAck запоминает подтверждение для отправки наверх.
func (u *Uplink) QueueAck(a telemetry.Ack) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending = append(u.pending, a)
}

// Run выгружает по расписанию до отмены контекста.
func (u *Uplink) Run(ctx context.Context) {
	if u.cfg.Center.URL == "" {
		u.log.Warn("центр не задан, работаем автономно: данные копятся в буфере")
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(u.cfg.Center.Every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Последняя попытка перед остановкой: то, что успели принять,
			// лучше отдать сразу, чем оставить до следующего запуска.
			u.once(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
			u.once(ctx)
		}
	}
}

func (u *Uplink) once(ctx context.Context) {
	u.flushAcks(ctx)

	// Пустой буфер не повод молчать: команды приходят только в ответе
	// на обращение сборщика, и без него управление перестало бы
	// работать, стоило платам замолчать.
	records := u.buf.Peek(u.cfg.Center.MaxBatch)

	batch := telemetry.Batch{
		SiteID:  u.cfg.Site,
		BatchID: newBatchID(),
		Packets: records,
		// Кто выгружает. На объекте сборщиков три, по одному на
		// помещение, а очередь команд общая: без этого поля первый
		// опросивший забрал бы чужие команды, и у соседа они бы
		// просто пропали.
		CollectorID: u.cfg.CollectorID(),
	}

	res, err := u.post(ctx, "/api/v1/telemetry", batch)
	if err != nil {
		u.fail(err)
		return
	}

	// Повтор уже принятого батча — тоже успех: центр отбросил дубль,
	// и записи можно вычищать.
	if len(records) > 0 {
		if err := u.buf.Commit(len(records)); err != nil {
			u.log.Error("очистка буфера", "err", err)
		}
	}

	u.ok()
	u.log.Debug("батч выгружен", "records", len(records),
		"accepted", res.Accepted, "duplicate", res.Duplicate, "left", u.buf.Len())

	for _, cmd := range res.Commands {
		ack, err := u.dispatch.Dispatch(cmd)
		if err != nil {
			u.log.Error("команда не доставлена", "id", cmd.ID, "kind", cmd.Kind, "err", err)
			// Центр должен узнать о недоставленной команде, иначе она
			// висела бы в панели «в пути» до бесконечности.
			u.QueueAck(telemetry.Ack{
				ID:     cmd.ID,
				Reason: err.Error(),
			})
			continue
		}
		// Команду исполнил сам сборщик — подтверждение готово сразу.
		if ack != nil {
			u.QueueAck(*ack)
		}
	}
}

// flushAcks отдаёт наверх подтверждения, собранные с плат.
func (u *Uplink) flushAcks(ctx context.Context) {
	u.mu.Lock()
	acks := u.pending
	u.pending = nil
	u.mu.Unlock()

	if len(acks) == 0 {
		return
	}

	if _, err := u.post(ctx, "/api/v1/acks", acks); err != nil {
		// Подтверждения не потеряем: вернём в очередь до следующего раза.
		u.mu.Lock()
		u.pending = append(acks, u.pending...)
		u.mu.Unlock()
		u.log.Warn("подтверждения не отправлены", "count", len(acks), "err", err)
	}
}

func (u *Uplink) post(ctx context.Context, path string, payload any) (telemetry.BatchResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return telemetry.BatchResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.cfg.Center.URL+path,
		bytes.NewReader(body))
	if err != nil {
		return telemetry.BatchResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if u.cfg.Center.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.cfg.Center.Token)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return telemetry.BatchResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return telemetry.BatchResult{}, nil
	}

	// 4xx означает, что батч негоден: повторять его бессмысленно, он
	// будет отвергнут снова. Но и удалять данные молча нельзя — пишем в
	// журнал и оставляем в буфере до разбора.
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return telemetry.BatchResult{}, fmt.Errorf("центр ответил %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}

	var res telemetry.BatchResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return telemetry.BatchResult{}, fmt.Errorf("ответ центра не разобран: %w", err)
	}
	return res, nil
}

func (u *Uplink) fail(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.attempts++
	u.lastErr = err

	// Не шумим на каждой попытке: канал может лежать часами, и журнал
	// не должен состоять из одной этой строки.
	if u.attempts == 1 || u.attempts%12 == 0 {
		u.log.Warn("выгрузка не удалась, данные копятся",
			"attempts", u.attempts, "buffered", u.buf.Len(), "err", err)
	}
}

func (u *Uplink) ok() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.attempts > 0 {
		u.log.Info("связь с центром восстановлена", "после_попыток", u.attempts)
	}
	u.attempts = 0
	u.lastErr = nil
	u.lastOK = time.Now()
}

// Status — состояние канала наверх, для локальной панели.
type Status struct {
	Center   string    `json:"center"`
	Buffered int       `json:"buffered"`
	LastOK   time.Time `json:"last_ok"`
	Attempts int       `json:"failed_attempts"`
	Error    string    `json:"error,omitempty"`
}

func (u *Uplink) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()

	s := Status{
		Center:   u.cfg.Center.URL,
		Buffered: u.buf.Len(),
		LastOK:   u.lastOK,
		Attempts: u.attempts,
	}
	if u.lastErr != nil {
		s.Error = u.lastErr.Error()
	}
	return s
}

func newBatchID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
