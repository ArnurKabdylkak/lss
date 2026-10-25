package collector

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/queue"
	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
	"github.com/ArnurKabdylkak/lss/center/internal/app/command"
	"github.com/ArnurKabdylkak/lss/center/internal/app/ingest"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

func newHandler(t *testing.T) (*Handler, *repo.Memory, *command.Service) {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	state := repo.NewMemory()
	cmds := command.New(queue.NewMemory(), log)
	ing := ingest.New(state, nil, nil, log, ingest.Options{})

	return New(ing, cmds, log), state, cmds
}

func post(t *testing.T, h *Handler, b telemetry.Batch) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	w := httptest.NewRecorder()
	h.Post(w, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", bytes.NewReader(body)))
	return w
}

func batch(id string, packets ...telemetry.Record) telemetry.Batch {
	return telemetry.Batch{SiteID: "obj-17", BatchID: id, Packets: packets}
}

func record(device string, t telemetry.Telemetry) telemetry.Record {
	return telemetry.Record{DeviceID: device, Received: time.Now(), Source: "192.168.1.50:5006", Data: t}
}

func TestБатчПринят(t *testing.T) {
	h, state, _ := newHandler(t)

	w := post(t, h, batch("b1",
		record("stm32-01", telemetry.Telemetry{RPM: 1500, TempC: 23.5}),
		record("stm32-02", telemetry.Telemetry{RPM: 900, TempC: 25}),
	))

	if w.Code != http.StatusOK {
		t.Fatalf("код %d, тело %s", w.Code, w.Body)
	}

	var res telemetry.BatchResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.Accepted != 2 {
		t.Errorf("принято %d записей из 2", res.Accepted)
	}

	devices, _ := state.List(t.Context())
	if len(devices) != 2 {
		t.Errorf("в хранилище %d устройств из 2", len(devices))
	}
	// Идентичность ставит сборщик; центр её не переопределяет адресом.
	if devices[0].Device != "stm32-01" {
		t.Errorf("идентификатор устройства потерян: %q", devices[0].Device)
	}
}

// Повтор неподтверждённой отправки не должен порождать дублей.
func TestПовторБатчаНеДублируетДанные(t *testing.T) {
	h, state, _ := newHandler(t)
	b := batch("b1", record("stm32-01", telemetry.Telemetry{RPM: 1500}))

	first := post(t, h, b)
	second := post(t, h, b)

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("коды %d и %d", first.Code, second.Code)
	}

	var res telemetry.BatchResult
	json.Unmarshal(second.Body.Bytes(), &res)
	if !res.Duplicate {
		t.Error("повтор не распознан")
	}
	if res.Accepted != 0 {
		t.Errorf("повтор принял %d записей", res.Accepted)
	}

	devices, _ := state.List(t.Context())
	if len(devices) != 1 {
		t.Errorf("устройств %d, ожидалось 1", len(devices))
	}
}

// Батч проверяется целиком: иначе часть записей осядет в центре,
// сборщик переотправит батч, и они задвоятся.
func TestБитаяЗаписьОтвергаетВесьБатч(t *testing.T) {
	h, state, _ := newHandler(t)

	w := post(t, h, batch("b1",
		record("stm32-01", telemetry.Telemetry{RPM: 1500}),
		record("stm32-02", telemetry.Telemetry{Event: telemetry.EventKey}), // без key
	))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400", w.Code)
	}
	if devices, _ := state.List(t.Context()); len(devices) != 0 {
		t.Errorf("в хранилище осело %d записей из отвергнутого батча", len(devices))
	}
}

// Команды доезжают до сборщика в ответе на выгрузку: другого пути нет.
func TestКомандыВозвращаютсяВОтветеНаВыгрузку(t *testing.T) {
	h, _, cmds := newHandler(t)

	_, err := cmds.Issue(t.Context(), "obj-17", telemetry.Command{
		Device: "stm32-01", Kind: telemetry.CmdHeaterOff, Operator: "дежурный"})
	if err != nil {
		t.Fatalf("постановка команды: %v", err)
	}

	w := post(t, h, batch("b1", record("stm32-01", telemetry.Telemetry{RPM: 1500})))

	var res telemetry.BatchResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Commands) != 1 {
		t.Fatalf("возвращено %d команд, ожидалась 1", len(res.Commands))
	}
	if res.Commands[0].Kind != telemetry.CmdHeaterOff {
		t.Errorf("не та команда: %+v", res.Commands[0])
	}
}

func TestПодтвержденияЗакрываютКоманды(t *testing.T) {
	h, _, cmds := newHandler(t)

	issued, _ := cmds.Issue(t.Context(), "obj-17", telemetry.Command{
		Device: "stm32-01", Kind: telemetry.CmdHeaterOn})

	body, _ := json.Marshal([]telemetry.Ack{{
		ID: issued.ID, Accepted: false, Reason: "overtemp", State: "off"}})

	w := httptest.NewRecorder()
	h.Ack(w, httptest.NewRequest(http.MethodPost, "/api/v1/acks", bytes.NewReader(body)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("код %d", w.Code)
	}

	pending, _ := cmds.Pending(t.Context(), "obj-17")
	if len(pending) != 1 || !pending[0].Delivered() {
		t.Fatal("команда не закрыта подтверждением")
	}
	// Отказ защиты — не потеря доставки: это осознанное решение платы.
	if pending[0].Ack.Accepted {
		t.Error("отказ записан как исполнение")
	}
}

// Команды доезжают только в ответе на запрос сборщика. Если бы он
// обращался в центр лишь при наличии данных, молчащие платы означали
// бы, что управление перестало работать.
func TestОпросКомандБезДанных(t *testing.T) {
	h, state, cmds := newHandler(t)

	_, err := cmds.Issue(t.Context(), "obj-17", telemetry.Command{
		Device: "stm32-01", Kind: telemetry.CmdDrop, Obstacle: 1})
	if err != nil {
		t.Fatalf("постановка команды: %v", err)
	}

	w := post(t, h, telemetry.Batch{SiteID: "obj-17", BatchID: "poll-1"})

	if w.Code != http.StatusOK {
		t.Fatalf("опрос отвергнут: код %d, тело %s", w.Code, w.Body)
	}

	var res telemetry.BatchResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Commands) != 1 {
		t.Fatalf("опрос вернул %d команд, ожидалась 1", len(res.Commands))
	}
	if devices, _ := state.List(t.Context()); len(devices) != 0 {
		t.Errorf("опрос добавил %d устройств", len(devices))
	}
}
