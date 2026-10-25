// Package video — прикладной сценарий видеонаблюдения: поднять
// трансляцию каждой включённой камеры и держать её поднятой.
//
// Камера на объекте пропадает по десятку причин — питание, сеть,
// перезагрузка регистратора. Оператор не должен нажимать «обновить»:
// надзор сам поднимает конвейер заново с нарастающей паузой.
package video

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

// Options настраивает надзор.
type Options struct {
	// MinBackoff, MaxBackoff — пауза перед перезапуском упавшей
	// трансляции. Растёт вдвое до предела, чтобы недоступная камера
	// не создавала постоянную нагрузку на сеть и на процессор.
	MinBackoff, MaxBackoff time.Duration
}

func (o Options) withDefaults() Options {
	if o.MinBackoff <= 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = 30 * time.Second
	}
	return o
}

// Supervisor держит трансляции камер и знает их состояние.
type Supervisor struct {
	cams port.CameraRepository
	pub  port.StreamPublisher
	log  *slog.Logger
	opts Options

	mu     sync.RWMutex
	states map[video.CameraID]*video.State
	order  []video.CameraID
}

func New(cams port.CameraRepository, pub port.StreamPublisher,
	log *slog.Logger, opts Options) *Supervisor {

	return &Supervisor{
		cams:   cams,
		pub:    pub,
		log:    log,
		opts:   opts.withDefaults(),
		states: make(map[video.CameraID]*video.State),
	}
}

// Run поднимает трансляции и возвращается, когда все конвейеры
// остановлены по контексту.
func (s *Supervisor) Run(ctx context.Context) error {
	cameras, err := s.cams.Cameras(ctx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, cam := range cameras {
		s.init(cam)

		if !cam.Enabled {
			s.log.Info("камера выключена в конфигурации", "camera", cam.ID)
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.keepAlive(ctx, cam)
		}()
	}

	s.log.Info("видеонаблюдение запущено", "cameras", len(cameras), "enabled", s.enabledCount())
	wg.Wait()
	return nil
}

// keepAlive ведёт одну камеру: запуск, наблюдение, перезапуск.
func (s *Supervisor) keepAlive(ctx context.Context, cam video.Camera) {
	backoff := s.opts.MinBackoff

	for {
		if ctx.Err() != nil {
			s.set(cam.ID, video.StatusStopped, "")
			return
		}

		s.set(cam.ID, video.StatusStarting, "")
		s.log.Info("запуск трансляции", "camera", cam.ID, "url", cam.SafeURL())

		started := time.Now()
		err := s.pub.Run(ctx, cam, func() {
			s.set(cam.ID, video.StatusLive, "")
			s.log.Info("камера в эфире", "camera", cam.ID,
				"за", time.Since(started).Round(time.Millisecond))
		})

		if ctx.Err() != nil {
			s.set(cam.ID, video.StatusStopped, "")
			s.log.Info("трансляция остановлена", "camera", cam.ID)
			return
		}

		reason := "конвейер завершился сам"
		if err != nil {
			reason = err.Error()
		}
		s.fail(cam.ID, reason)
		s.log.Error("трансляция сорвалась", "camera", cam.ID, "err", reason,
			"продержалась", time.Since(started).Round(time.Second))

		// Долго прожившая трансляция — это разовый сбой, а не
		// недоступная камера: счётчик паузы для неё сбрасывается.
		if time.Since(started) > s.opts.MaxBackoff {
			backoff = s.opts.MinBackoff
		}

		select {
		case <-time.After(jitter(backoff)):
		case <-ctx.Done():
			s.set(cam.ID, video.StatusStopped, "")
			return
		}

		if backoff < s.opts.MaxBackoff {
			backoff = min(backoff*2, s.opts.MaxBackoff)
		}
	}
}

// jitter разводит перезапуски во времени: иначе после провала сети
// все камеры объекта ломятся к регистратору одной секундой.
func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int64N(int64(d/2)+1))
}

// States возвращает состояние всех камер в порядке из конфигурации.
func (s *Supervisor) States() []video.State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]video.State, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.states[id])
	}
	return out
}

// State возвращает состояние одной камеры.
func (s *Supervisor) State(id video.CameraID) (video.State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.states[id]
	if !ok {
		return video.State{}, false
	}
	return *st, true
}

// PlaylistURL — адрес, по которому панель открывает камеру.
func (s *Supervisor) PlaylistURL(id video.CameraID) string {
	return s.pub.PlaylistURL(id)
}

func (s *Supervisor) init(cam video.Camera) {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := video.StatusStarting
	if !cam.Enabled {
		status = video.StatusDisabled
	}
	if _, ok := s.states[cam.ID]; !ok {
		s.order = append(s.order, cam.ID)
	}
	s.states[cam.ID] = &video.State{Camera: cam, Status: status, Since: time.Now()}
}

func (s *Supervisor) set(id video.CameraID, status video.Status, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.states[id]
	if !ok {
		return
	}
	if st.Status != status {
		st.Since = time.Now()
	}
	st.Status = status
	if errText != "" {
		st.LastError = errText
	}
}

func (s *Supervisor) fail(id video.CameraID, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.states[id]
	if !ok {
		return
	}
	st.Status = video.StatusFailed
	st.Since = time.Now()
	st.LastError = reason
	st.Restarts++
}

func (s *Supervisor) enabledCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var n int
	for _, st := range s.states {
		if st.Camera.Enabled {
			n++
		}
	}
	return n
}
