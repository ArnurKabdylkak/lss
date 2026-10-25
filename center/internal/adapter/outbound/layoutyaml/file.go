// Package layoutyaml читает раскладку объекта из YAML: комнаты, их
// тренажёры, уставки пушки и камеры.
//
// Один файл на объект: когда камеры лежали отдельно, связать камеру с
// комнатой можно было только по памяти инженера.
package layoutyaml

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
	"gopkg.in/yaml.v3"
)

type file struct {
	Site     string    `yaml:"site"`
	Rooms    []room    `yaml:"rooms"`
	Sections []section `yaml:"sections"`
}

// section — группа комнат, на которые открывается одно занятие.
type section struct {
	ID    string   `yaml:"id"`
	Name  string   `yaml:"name"`
	Rooms []string `yaml:"rooms"`
}

type room struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name"`
	Devices   []device `yaml:"devices"`
	Capacity  int      `yaml:"capacity"`
	Collector string   `yaml:"collector"`
	Sensor    string   `yaml:"sensor"`
	// Equipment — узлы помещения. Допускаются две записи: короткая
	// («light»), когда узел ни в подготовке, ни в аварии не участвует,
	// и полная — с ролью в сценарии.
	Floor     int        `yaml:"floor"`
	Equipment []unitSpec `yaml:"equipment"`
	Heater    *heater    `yaml:"heater"`
	Prepare   *prepare   `yaml:"prepare"`
	Tasks     []task     `yaml:"tasks"`
	Cameras   []camera   `yaml:"cameras"`
}

// task — задание маршрута. device задаёт автоматическую отметку:
// по сигналу этого устройства задание засчитывается само.
type task struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Device string `yaml:"device"`
	Note   string `yaml:"note"`
}

// prepare — подготовка помещения перед тренировкой.
//
// Цель задаётся не всем комнатам: у дымокамеры её нет, температура
// там показывается для контроля (сценарий дымокамеры §2).
type prepare struct {
	TargetC float64 `yaml:"target_c"`
	Minutes int     `yaml:"minutes"`
}

type device struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
	// StepsPerFloor — правило пересчёта ступеней в этажи у лестницы.
	StepsPerFloor int `yaml:"steps_per_floor"`
}

type heater struct {
	Device  string  `yaml:"device"`
	LimitC  float64 `yaml:"limit_c"`
	ResumeC float64 `yaml:"resume_c"`
}

type camera struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	URL       string `yaml:"url"`
	Transport string `yaml:"transport"`
	Location  string `yaml:"location"`
	Audio     bool   `yaml:"audio"`
	// Disabled, а не Enabled: забытое поле не должно гасить камеру.
	Disabled bool `yaml:"disabled"`
}

// Repository — раскладка, прочитанная при старте. Реализует
// port.LayoutRepository и port.CameraRepository: видеотракту нужны
// только камеры, панели — комнаты целиком.
type Repository struct {
	path   string
	object layout.Object
}

// Load читает файл и проверяет его целиком. Одна негодная запись валит
// загрузку: половина поднятого объекта хуже явной ошибки при старте.
func Load(path string) (*Repository, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение раскладки объекта: %w", err)
	}

	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}

	obj := layout.Object{Site: f.Site, Rooms: make([]layout.Room, 0, len(f.Rooms))}

	for _, r := range f.Rooms {
		room := layout.Room{
			ID:        layout.RoomID(r.ID),
			Name:      r.Name,
			Capacity:  r.Capacity,
			Collector: r.Collector,
			Sensor:    r.Sensor,
			Floor:     r.Floor,
			Equipment: units(r.Equipment),
			Devices:   make([]layout.Device, 0, len(r.Devices)),
			Cameras:   make([]video.Camera, 0, len(r.Cameras)),
		}

		for _, d := range r.Devices {
			room.Devices = append(room.Devices, layout.Device{
				ID:            d.ID,
				Name:          d.Name,
				Kind:          layout.Kind(d.Kind),
				StepsPerFloor: d.StepsPerFloor,
			})
		}

		for _, t := range r.Tasks {
			room.Tasks = append(room.Tasks, layout.Task{
				ID: t.ID, Name: t.Name, Device: t.Device, Note: t.Note,
			})
		}

		if r.Prepare != nil {
			room.Prepare = layout.Prepare{
				TargetC: r.Prepare.TargetC,
				Timeout: time.Duration(r.Prepare.Minutes) * time.Minute,
			}
		}

		if r.Heater != nil {
			room.Heater = layout.Heater{
				Device:  r.Heater.Device,
				LimitC:  r.Heater.LimitC,
				ResumeC: r.Heater.ResumeC,
			}
		}

		for _, c := range r.Cameras {
			room.Cameras = append(room.Cameras, video.Camera{
				ID:        video.CameraID(c.ID),
				Name:      c.Name,
				URL:       os.ExpandEnv(c.URL), // пароль можно держать в окружении
				Transport: transportOr(c.Transport, video.TransportTCP),
				Site:      f.Site,
				Room:      r.ID,
				Location:  c.Location,
				Audio:     c.Audio,
				Enabled:   !c.Disabled,
			})
		}

		obj.Rooms = append(obj.Rooms, room)
	}

	for _, sec := range f.Sections {
		item := layout.Section{ID: sec.ID, Name: sec.Name}
		for _, id := range sec.Rooms {
			item.Rooms = append(item.Rooms, layout.RoomID(id))
		}
		obj.Sections = append(obj.Sections, item)
	}

	if err := obj.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &Repository{path: path, object: obj}, nil
}

// Object возвращает раскладку целиком.
func (r *Repository) Object(context.Context) (layout.Object, error) {
	return r.object, nil
}

// Cameras отдаёт камеры всех комнат — для видеотракта.
func (r *Repository) Cameras(context.Context) ([]video.Camera, error) {
	return r.object.Cameras(), nil
}

// Path — откуда прочитана раскладка; нужен для журнала при старте.
func (r *Repository) Path() string { return r.path }

func transportOr(v string, def video.Transport) video.Transport {
	if v == "" {
		return def
	}
	return video.Transport(v)
}

// unitSpec разбирает узел помещения: строкой или таблицей.
type unitSpec struct {
	Unit    string
	Prepare bool
	Alarm   string
	Hold    time.Duration
	Cycle   time.Duration
}

func (u *unitSpec) UnmarshalYAML(value *yaml.Node) error {
	// Короткая запись: один идентификатор узла.
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&u.Unit)
	}

	var full struct {
		Unit    string        `yaml:"unit"`
		Prepare bool          `yaml:"prepare"`
		Alarm   string        `yaml:"alarm"`
		Hold    time.Duration `yaml:"hold"`
		Cycle   time.Duration `yaml:"cycle"`
	}
	if err := value.Decode(&full); err != nil {
		return err
	}
	if full.Unit == "" {
		return fmt.Errorf("узел помещения без имени")
	}
	u.Unit, u.Prepare, u.Alarm = full.Unit, full.Prepare, full.Alarm
	u.Hold, u.Cycle = full.Hold, full.Cycle
	return nil
}

func units(list []unitSpec) []layout.UnitSpec {
	out := make([]layout.UnitSpec, 0, len(list))
	for _, u := range list {
		out = append(out, layout.UnitSpec{
			Unit: u.Unit, Prepare: u.Prepare, Alarm: u.Alarm,
			Hold: u.Hold, Cycle: u.Cycle,
		})
	}
	return out
}
