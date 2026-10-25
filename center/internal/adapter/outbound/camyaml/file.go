// Package camyaml читает описание камер из YAML-файла.
//
// Камеры заводит инженер на объекте, а не программист: список лежит
// рядом с сервисом в понятном текстовом виде и правится без пересборки.
package camyaml

import (
	"context"
	"fmt"
	"os"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
	"gopkg.in/yaml.v3"
)

// file — формат файла. Держим его отдельно от доменной модели:
// на диске удобны «мягкие» значения и умолчания, в домене — строгие.
type file struct {
	Cameras []camera `yaml:"cameras"`
}

type camera struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	URL       string `yaml:"url"`
	Transport string `yaml:"transport"`
	Site      string `yaml:"site"`
	Location  string `yaml:"location"`
	Audio     bool   `yaml:"audio"`
	// Disabled, а не Enabled: умолчание нулевого значения — «камера
	// работает», иначе забытое поле молча гасит объект.
	Disabled bool `yaml:"disabled"`
}

// Repository — список камер, прочитанный из файла при старте.
type Repository struct {
	path    string
	cameras []video.Camera
}

// Load читает и проверяет файл целиком. Одна негодная запись валит
// загрузку: половина поднятого видеонаблюдения хуже явной ошибки
// при старте, потому что о пропавшей камере никто не узнает.
func Load(path string) (*Repository, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение списка камер: %w", err)
	}

	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}

	seen := make(map[video.CameraID]struct{}, len(f.Cameras))
	cameras := make([]video.Camera, 0, len(f.Cameras))

	for i, c := range f.Cameras {
		cam := video.Camera{
			ID:        video.CameraID(c.ID),
			Name:      c.Name,
			URL:       os.ExpandEnv(c.URL), // пароль можно держать в окружении
			Transport: transportOr(c.Transport, video.TransportTCP),
			Site:      c.Site,
			Location:  c.Location,
			Audio:     c.Audio,
			Enabled:   !c.Disabled,
		}
		if err := cam.Validate(); err != nil {
			return nil, fmt.Errorf("%s, камера №%d: %w", path, i+1, err)
		}
		if _, dup := seen[cam.ID]; dup {
			return nil, fmt.Errorf("%s: идентификатор %q встречается дважды", path, cam.ID)
		}
		seen[cam.ID] = struct{}{}
		cameras = append(cameras, cam)
	}

	return &Repository{path: path, cameras: cameras}, nil
}

func (r *Repository) Cameras(context.Context) ([]video.Camera, error) {
	out := make([]video.Camera, len(r.cameras))
	copy(out, r.cameras)
	return out, nil
}

// Path — откуда прочитан список; нужен для журнала при старте.
func (r *Repository) Path() string { return r.path }

func transportOr(v string, def video.Transport) video.Transport {
	if v == "" {
		return def
	}
	return video.Transport(v)
}
