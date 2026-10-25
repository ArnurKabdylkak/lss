package camyaml

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cameras.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	t.Setenv("CAM_PASS", "s3cret")

	path := write(t, `
cameras:
  - id: cam-101
    name: "Въезд"
    url: "rtsp://admin:${CAM_PASS}@192.168.1.64:554/Streaming/channels/101"
    location: "КПП"
  - id: cam-102
    url: "rtsp://admin:pass@192.168.1.65:554/Streaming/channels/101"
    transport: udp
    disabled: true
`)

	repo, err := Load(path)
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	cams, err := repo.Cameras(context.Background())
	if err != nil {
		t.Fatalf("Cameras() вернул ошибку: %v", err)
	}
	if len(cams) != 2 {
		t.Fatalf("прочитано %d камер, ожидалось 2", len(cams))
	}

	first := cams[0]
	if first.Transport != video.TransportTCP {
		t.Errorf("транспорт по умолчанию = %q, ожидался tcp", first.Transport)
	}
	if !first.Enabled {
		t.Error("камера без disabled должна быть включена")
	}
	if first.URL != "rtsp://admin:s3cret@192.168.1.64:554/Streaming/channels/101" {
		t.Errorf("пароль из окружения не подставлен: %s", first.SafeURL())
	}
	if got := first.SafeURL(); got != "rtsp://admin:xxxxx@192.168.1.64:554/Streaming/channels/101" {
		t.Errorf("SafeURL() = %q, пароль должен быть скрыт", got)
	}
	if cams[1].Enabled {
		t.Error("камера с disabled: true должна быть выключена")
	}
}

func TestLoadRejectsBadFile(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"повтор идентификатора": `
cameras:
  - {id: cam-1, url: "rtsp://h/1"}
  - {id: cam-1, url: "rtsp://h/2"}`,
		"пустой идентификатор":  "cameras:\n  - {url: \"rtsp://h/1\"}",
		"идентификатор с путём": `cameras: [{id: "../etc", url: "rtsp://h/1"}]`,
		"неизвестная схема":     `cameras: [{id: cam-1, url: "file:///etc/passwd"}]`,
		"неизвестный транспорт": `cameras: [{id: cam-1, url: "rtsp://h/1", transport: carrier}]`,
		"адрес без хоста":       `cameras: [{id: cam-1, url: "rtsp:///1"}]`,
		"это вообще не YAML":    "cameras: [",
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("ожидалась ошибка загрузки")
			}
		})
	}
}
