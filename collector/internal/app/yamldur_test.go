package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Длительности в конфигурации задаются как "5s". Если разбор их не
// понимает, все таймеры молча становятся нулевыми — это надо знать.
func TestДлительностиРазбираются(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yml")
	os.WriteFile(path, []byte(`
site: obj-17
listen: ":5005"
silence: 3s
center:
  url: "http://x"
  every: 1s
  timeout: 10s
outputs:
  enabled: true
  lines:
    - id: heater-1
      line: 17
      kind: heater
      limit_c: 60
      restore_c: 50
      min_off: 0s
      board_ttl: 5s
`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("разбор конфигурации: %v", err)
	}
	if cfg.Silence != 3*time.Second {
		t.Errorf("silence разобран как %v", cfg.Silence)
	}
	if cfg.Center.Every != time.Second {
		t.Errorf("center.every разобран как %v", cfg.Center.Every)
	}
	if len(cfg.Outputs.Lines) != 1 {
		t.Fatalf("выходы разобраны как %+v", cfg.Outputs.Lines)
	}
	out := cfg.Outputs.Lines[0]
	if out.BoardTTL != 5*time.Second {
		t.Errorf("board_ttl разобран как %v", out.BoardTTL)
	}
	// Ноль в файле — это «без выдержки», и отличаться от «поля нет»
	// он обязан: иначе умолчание молча вернёт 30 секунд.
	if out.MinOff == nil || *out.MinOff != 0 {
		t.Errorf("min_off разобран как %v", out.MinOff)
	}
	if out.Limits().MinOff != 0 {
		t.Errorf("нулевая выдержка подменена умолчанием: %v", out.Limits().MinOff)
	}
}
