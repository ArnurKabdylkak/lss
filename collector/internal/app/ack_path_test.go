package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

type stubDispatch struct{}

func (stubDispatch) Dispatch(cmd telemetry.Command) (*telemetry.Ack, error) {
	return &telemetry.Ack{ID: cmd.ID, Accepted: true, Reason: "ok", State: "on"}, nil
}

// Подтверждение исполненной команды должно дойти до центра. Без него
// узел висит в ожидании: панель мигает, пульт перестаёт его слушать.
func TestПодтверждениеУходитВЦентр(t *testing.T) {
	var mu sync.Mutex
	acksSeen := 0
	var payload []telemetry.Ack

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/telemetry":
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(telemetry.BatchResult{
				Commands: []telemetry.Command{{
					ID: "cmd-1", Kind: telemetry.CmdEquipment,
					Unit: "light", State: "on",
				}},
			})
		case "/api/v1/acks":
			var got []telemetry.Ack
			_ = json.NewDecoder(r.Body).Decode(&got)
			mu.Lock()
			acksSeen++
			payload = got
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := Config{Site: "obj-17"}
	cfg.Center.URL = srv.URL
	cfg.Center.Timeout = 2 * time.Second
	cfg.Center.MaxBatch = 10

	buf, err := OpenBuffer(filepath.Join(t.TempDir(), "buffer.jsonl"), 100)
	if err != nil {
		t.Fatal(err)
	}

	u := NewUplink(cfg, buf, stubDispatch{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Первый проход: центр отдаёт команду, сборщик её исполняет и
	// ставит подтверждение в очередь.
	u.once(t.Context())

	mu.Lock()
	first := acksSeen
	mu.Unlock()
	if first != 0 {
		t.Fatalf("подтверждение ушло до того, как появилось: %d", first)
	}

	// Второй проход: очередь должна уйти наверх.
	u.once(t.Context())

	mu.Lock()
	defer mu.Unlock()
	if acksSeen == 0 {
		t.Fatal("подтверждение так и не отправлено в центр")
	}
	if len(payload) != 1 || payload[0].ID != "cmd-1" {
		t.Fatalf("центр получил не то: %+v", payload)
	}
	if !payload[0].Accepted {
		t.Error("подтверждение дошло как отказ")
	}
}
