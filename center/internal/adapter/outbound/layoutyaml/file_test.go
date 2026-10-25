package layoutyaml

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rooms.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}
	return path
}

const sample = `
site: obj-17
rooms:
  - id: room-1
    name: "Тренажёрный зал"
    devices:
      - {id: obj-17-bike-1, name: "Велосипед", kind: bike}
      - {id: obj-17-stairs-1, name: "Лестница", kind: stairs}
      - {id: obj-17-load-1, kind: load}
    heater: {device: obj-17-heater-1, limit_c: 60, resume_c: 50}
    cameras:
      - {id: room1-cam-1, name: "Общий план", url: "rtsp://admin:pass@192.168.1.64:554/s/101"}
      - {id: room1-cam-2, url: "rtsp://admin:pass@192.168.1.65:554/s/101", disabled: true}
  - id: room-2
    name: "Пустая"
`

func TestLoad(t *testing.T) {
	t.Parallel()

	repo, err := Load(write(t, sample))
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}

	obj, err := repo.Object(context.Background())
	if err != nil {
		t.Fatalf("Object() вернул ошибку: %v", err)
	}
	if len(obj.Rooms) != 2 {
		t.Fatalf("комнат прочитано %d, ожидалось 2", len(obj.Rooms))
	}

	room := obj.Rooms[0]
	if len(room.Devices) != 3 {
		t.Errorf("устройств в комнате %d, ожидалось 3", len(room.Devices))
	}
	if room.Devices[2].Title() != "ударный молот" {
		t.Errorf("устройство без имени должно называться по виду, получено %q", room.Devices[2].Title())
	}
	if !room.Devices[1].Kind.Draft() {
		t.Error("лестница должна быть помечена как черновик контракта")
	}
	if room.Heater.LimitC != 60 || room.Heater.ResumeC != 50 {
		t.Errorf("уставки пушки прочитаны неверно: %+v", room.Heater)
	}

	// Пушка входит в состав комнаты наравне с тренажёрами.
	ids := room.DeviceIDs()
	if len(ids) != 4 || ids[3] != "obj-17-heater-1" {
		t.Errorf("DeviceIDs() = %v", ids)
	}

	cams, err := repo.Cameras(context.Background())
	if err != nil || len(cams) != 2 {
		t.Fatalf("Cameras() = %d шт., err %v", len(cams), err)
	}
	if cams[0].Room != "room-1" {
		t.Errorf("камере не проставлена комната: %+v", cams[0])
	}
	if cams[1].Enabled {
		t.Error("камера с disabled: true должна быть выключена")
	}

	if r, ok := obj.RoomOfDevice("obj-17-heater-1"); !ok || r.ID != layout.RoomID("room-1") {
		t.Error("пушка не нашлась по комнате")
	}
}

func TestLoadRejectsBadLayout(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"неизвестный вид тренажёра": `
rooms: [{id: room-1, devices: [{id: d1, kind: treadmill}]}]`,
		"возврат выше порога": `
rooms: [{id: room-1, heater: {device: h1, limit_c: 50, resume_c: 60}}]`,
		"порог не задан": `
rooms: [{id: room-1, heater: {device: h1}}]`,
		"комната дважды": `
rooms: [{id: room-1}, {id: room-1}]`,
		"устройство в двух комнатах": `
rooms:
  - {id: room-1, devices: [{id: d1, kind: bike}]}
  - {id: room-2, devices: [{id: d1, kind: bike}]}`,
		"камера в двух комнатах": `
rooms:
  - {id: room-1, cameras: [{id: c1, url: "rtsp://h/1"}]}
  - {id: room-2, cameras: [{id: c1, url: "rtsp://h/2"}]}`,
		"комната без идентификатора": `
rooms: [{name: "Зал"}]`,
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
