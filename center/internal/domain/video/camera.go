// Package video — предметная область видеонаблюдения: камеры объекта и
// состояние их трансляций. Зависимостей, кроме стандартной библиотеки,
// у пакета нет: ни ffmpeg, ни HTTP он не знает.
package video

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrInvalidCamera — описание камеры в конфигурации непригодно к работе.
var ErrInvalidCamera = errors.New("некорректное описание камеры")

// CameraID — идентификатор камеры из конфигурации. Попадает в адрес
// трансляции и в имя каталога с сегментами, поэтому набор символов узкий.
type CameraID string

// Transport — транспорт нижнего уровня для RTSP. TCP переживает потери
// на загруженной сети, UDP даёт меньшую задержку.
type Transport string

const (
	TransportTCP Transport = "tcp"
	TransportUDP Transport = "udp"
)

// Camera — источник видео на объекте.
type Camera struct {
	ID        CameraID
	Name      string
	URL       string // rtsp://логин:пароль@хост:554/...
	Transport Transport
	Site      string // объект, к которому относится камера
	Room      string // комната объекта; пусто — камера вне комнат
	Location  string // где стоит: «въезд», «щитовая»
	Audio     bool   // пробрасывать ли звук
	Enabled   bool
}

// Validate проверяет описание до попытки запуска: ошибка в конфигурации
// должна быть видна при старте, а не в виде молча падающего ffmpeg.
func (c Camera) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("%w: пустой идентификатор", ErrInvalidCamera)
	}
	if !safeID(string(c.ID)) {
		return fmt.Errorf("%w: идентификатор %q — допустимы латиница, цифры, «-» и «_» (до 64 символов)",
			ErrInvalidCamera, c.ID)
	}

	u, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("%w: камера %s: адрес не разобран: %v", ErrInvalidCamera, c.ID, err)
	}
	switch u.Scheme {
	case "rtsp", "rtsps", "http", "https":
	default:
		return fmt.Errorf("%w: камера %s: неподдерживаемая схема %q", ErrInvalidCamera, c.ID, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: камера %s: в адресе нет хоста", ErrInvalidCamera, c.ID)
	}

	switch c.Transport {
	case TransportTCP, TransportUDP:
	default:
		return fmt.Errorf("%w: камера %s: неизвестный транспорт %q", ErrInvalidCamera, c.ID, c.Transport)
	}
	return nil
}

// SafeURL — адрес без пароля. В логи, API и сообщения об ошибках уходит
// только он: в RTSP-ссылке лежат учётные данные камеры.
func (c Camera) SafeURL() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return "(адрес не разобран)"
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			u.User = url.UserPassword(name, "xxxxx")
		}
	}
	return u.String()
}

// Title — человекочитаемое имя камеры для панели.
func (c Camera) Title() string {
	if c.Name != "" {
		return c.Name
	}
	return string(c.ID)
}

func safeID(s string) bool {
	if s == "" || len(s) > 64 || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// Status — состояние конвейера трансляции.
type Status string

const (
	StatusDisabled Status = "disabled" // выключена в конфигурации
	StatusStarting Status = "starting" // процесс поднят, эфира ещё нет
	StatusLive     Status = "live"     // сегменты пишутся, можно смотреть
	StatusFailed   Status = "failed"   // сорвалась, ждёт перезапуска
	StatusStopped  Status = "stopped"  // сервис остановлен
)

// State — наблюдаемое состояние трансляции одной камеры.
type State struct {
	Camera    Camera
	Status    Status
	Since     time.Time // когда состояние стало текущим
	Restarts  int       // сколько раз конвейер поднимали заново
	LastError string    // причина последнего срыва
}

// Live сообщает, что зритель может подключиться прямо сейчас.
func (s State) Live() bool { return s.Status == StatusLive }
