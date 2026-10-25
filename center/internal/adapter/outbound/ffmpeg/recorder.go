package ffmpeg

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

// Запись занятия на диск.
//
// Отдельный процесс ffmpeg на камеру, параллельно трансляции: тянуть
// один поток на две задачи нельзя — обрыв записи уронил бы и Live.
// Видео пишется как есть, без перекодирования: камера уже отдаёт
// H.264, и ролик получается почти бесплатно по процессору.

// RecorderConfig — параметры записи.
type RecorderConfig struct {
	Binary string
	// Root — каталог, куда складываются ролики.
	Root string
	// Timeout — предел ожидания ответа камеры.
	Timeout time.Duration
	// MaxDuration — предел длительности одной записи. Защита от
	// занятия, которое забыли закрыть: иначе диск кончится молча.
	MaxDuration time.Duration
}

func (c RecorderConfig) withDefaults() RecorderConfig {
	if c.Binary == "" {
		c.Binary = "ffmpeg"
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.MaxDuration <= 0 {
		c.MaxDuration = 4 * time.Hour
	}
	return c
}

// Recorder пишет занятия в файлы.
type Recorder struct {
	cfg RecorderConfig
	log *slog.Logger

	mu      sync.Mutex
	running map[string][]*capture // занятие -> записи камер
}

// capture — одна идущая запись.
type capture struct {
	camera  video.CameraID
	file    string
	started time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	errTail *tail
	err     error
}

// NewRecorder проверяет окружение и готовит каталог.
func NewRecorder(cfg RecorderConfig, log *slog.Logger) (*Recorder, error) {
	cfg = cfg.withDefaults()

	if _, err := exec.LookPath(cfg.Binary); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFFmpegMissing, cfg.Binary)
	}
	if cfg.Root == "" {
		return nil, fmt.Errorf("не задан каталог для видеозаписей")
	}
	if err := os.MkdirAll(cfg.Root, 0o750); err != nil {
		return nil, fmt.Errorf("каталог видеозаписей: %w", err)
	}

	return &Recorder{cfg: cfg, log: log, running: make(map[string][]*capture)}, nil
}

// Root — каталог с роликами; его отдаёт наружу HTTP-адаптер.
func (r *Recorder) Root() string { return r.cfg.Root }

// Start начинает запись камер занятия.
func (r *Recorder) Start(ctx context.Context, session string, cameras []video.Camera) {
	if session == "" || len(cameras) == 0 {
		return
	}

	dir := filepath.Join(r.cfg.Root, session)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.log.Error("каталог записи не создан", "session", session, "err", err)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, cam := range cameras {
		if !cam.Enabled {
			continue
		}
		c := r.startOne(ctx, session, dir, cam)
		r.running[session] = append(r.running[session], c)
	}

	r.log.Info("начата видеозапись занятия", "session", session, "камер", len(r.running[session]))
}

func (r *Recorder) startOne(ctx context.Context, session, dir string, cam video.Camera) *capture {
	name := string(cam.ID) + ".mp4"
	path := filepath.Join(dir, name)

	// Контекст записи не наследует отмену вызывающего: занятие
	// закрывают явно, и запись не должна обрываться оттого, что
	// завершился HTTP-запрос.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	c := &capture{
		camera:  cam.ID,
		file:    filepath.Join(session, name),
		started: time.Now(),
		cancel:  cancel,
		done:    make(chan struct{}),
		errTail: &tail{},
	}

	cmd := exec.CommandContext(runCtx, r.cfg.Binary, r.args(cam, path)...)
	setGroup(cmd)
	// Мягко: ffmpeg дописывает файл и выходит, а убитый процесс
	// оставил бы ролик без хвоста.
	cmd.Cancel = func() error { return stopGroup(cmd, true) }
	cmd.WaitDelay = 10 * time.Second
	cmd.Stderr = c.errTail

	if err := cmd.Start(); err != nil {
		c.err = fmt.Errorf("запуск записи: %w", err)
		close(c.done)
		cancel()
		return c
	}

	go func() {
		defer close(c.done)
		if err := cmd.Wait(); err != nil && runCtx.Err() == nil {
			// Остановка по нашей команде ошибкой не считается.
			c.err = fmt.Errorf("%s", reason(c.errTail.String()))
			r.log.Error("запись прервалась",
				"session", session, "camera", cam.ID, "err", c.err)
		}
	}()

	return c
}

func (r *Recorder) args(cam video.Camera, path string) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "warning"}

	if strings.HasPrefix(cam.URL, "rtsp") {
		args = append(args,
			"-rtsp_transport", string(cam.Transport),
			"-timeout", fmt.Sprintf("%d", r.cfg.Timeout.Microseconds()),
		)
	}
	args = append(args, "-i", cam.URL)

	args = append(args, "-c:v", "copy")
	if cam.Audio {
		args = append(args, "-c:a", "aac", "-b:a", "64k")
	} else {
		args = append(args, "-an")
	}

	args = append(args,
		// Предел длительности: занятие могли забыть закрыть, и без
		// него запись шла бы, пока не кончится диск.
		"-t", fmt.Sprintf("%.0f", r.cfg.MaxDuration.Seconds()),
		// Фрагментированный MP4: если процесс всё же убьют, файл
		// останется воспроизводимым. У обычного MP4 заголовок
		// дописывается в конце, и оборванная запись пропадает целиком.
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", path,
	)
	return args
}

// Stop останавливает запись занятия и возвращает ролики.
func (r *Recorder) Stop(session string) []training.Record {
	r.mu.Lock()
	captures := r.running[session]
	delete(r.running, session)
	r.mu.Unlock()

	if len(captures) == 0 {
		return nil
	}

	out := make([]training.Record, 0, len(captures))
	for _, c := range captures {
		c.cancel()
		<-c.done

		rec := training.Record{
			Camera:   string(c.camera),
			File:     c.file,
			Started:  c.started,
			Finished: time.Now(),
		}
		if c.err != nil {
			rec.Error = c.err.Error()
		}
		if info, err := os.Stat(filepath.Join(r.cfg.Root, c.file)); err == nil {
			rec.Bytes = info.Size()
		} else if rec.Error == "" {
			rec.Error = "файл записи не создан"
		}

		out = append(out, rec)
	}

	r.log.Info("видеозапись занятия завершена", "session", session, "роликов", len(out))
	return out
}

// StopAll останавливает все записи — вызывается при остановке сервиса,
// чтобы ролики дописались, а не остались обрезанными.
func (r *Recorder) StopAll() {
	r.mu.Lock()
	sessions := make([]string, 0, len(r.running))
	for id := range r.running {
		sessions = append(sessions, id)
	}
	r.mu.Unlock()

	for _, id := range sessions {
		r.Stop(id)
	}
}
