// Package ffmpeg — выходной адаптер трансляции: берёт RTSP-поток камеры
// и раскладывает его в HLS, который играет любой браузер.
//
// Видео не перекодируется: камера уже отдаёт H.264, и мы лишь меняем
// упаковку. Это держит нагрузку на процессор около нуля и позволяет
// вести десятки камер на одном узле.
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

// Config — параметры раскладки HLS.
type Config struct {
	// Binary — чем запускать; по умолчанию ffmpeg из PATH.
	Binary string
	// Root — каталог, куда пишутся плейлисты и сегменты.
	Root string
	// Segment — длительность сегмента. Задержка у зрителя примерно
	// втрое больше: плеер набирает буфер, прежде чем начать.
	Segment time.Duration
	// Window — сколько сегментов держать в плейлисте и на диске.
	Window int
	// Stall — если плейлист не обновлялся дольше, поток считается
	// замершим и конвейер перезапускается. Ноль — не следить.
	Stall time.Duration
	// Timeout — предел ожидания ответа камеры при подключении.
	Timeout time.Duration
	// URLPrefix — префикс, под которым HTTP отдаёт каталог Root.
	URLPrefix string
}

func (c Config) withDefaults() Config {
	if c.Binary == "" {
		c.Binary = "ffmpeg"
	}
	if c.Segment <= 0 {
		c.Segment = 2 * time.Second
	}
	if c.Window <= 0 {
		c.Window = 6
	}
	if c.Stall <= 0 {
		c.Stall = 6 * c.Segment
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	if c.URLPrefix == "" {
		c.URLPrefix = "/video"
	}
	return c
}

// ErrFFmpegMissing — исполняемого файла нет; без него видео не поднять.
var ErrFFmpegMissing = errors.New("ffmpeg не найден")

// HLS реализует port.StreamPublisher поверх ffmpeg.
type HLS struct {
	cfg Config
	log *slog.Logger
}

// NewHLS проверяет окружение сразу: отсутствующий ffmpeg должен
// выясниться при старте сервиса, а не при первом взгляде оператора
// на чёрный квадрат.
func NewHLS(cfg Config, log *slog.Logger) (*HLS, error) {
	cfg = cfg.withDefaults()

	if _, err := exec.LookPath(cfg.Binary); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFFmpegMissing, cfg.Binary)
	}
	if cfg.Root == "" {
		return nil, fmt.Errorf("не задан каталог для сегментов видео")
	}
	if err := os.MkdirAll(cfg.Root, 0o750); err != nil {
		return nil, fmt.Errorf("каталог сегментов: %w", err)
	}

	return &HLS{cfg: cfg, log: log}, nil
}

// Root — каталог с сегментами; его отдаёт наружу HTTP-адаптер.
func (h *HLS) Root() string { return h.cfg.Root }

// PlaylistURL — путь к плейлисту камеры.
func (h *HLS) PlaylistURL(id video.CameraID) string {
	return fmt.Sprintf("%s/%s/index.m3u8", strings.TrimRight(h.cfg.URLPrefix, "/"), id)
}

// Run ведёт трансляцию камеры до остановки контекста или сбоя ffmpeg.
func (h *HLS) Run(ctx context.Context, cam video.Camera, onLive func()) error {
	dir := filepath.Join(h.cfg.Root, string(cam.ID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("каталог камеры: %w", err)
	}
	// Хвост прошлой трансляции ввёл бы плеер в заблуждение: он начал бы
	// с сегментов, которых уже нет на диске.
	if err := clean(dir); err != nil {
		return fmt.Errorf("очистка каталога камеры: %w", err)
	}

	playlist := filepath.Join(dir, "index.m3u8")

	// Метка запуска в имени сегмента.
	//
	// Сегменты отдаются как неизменяемые — иначе кеш не снимал бы
	// нагрузку. Но после перезапуска конвейера нумерация начинается
	// с нуля, и браузер, встретив знакомое имя, показал бы кадры
	// прошлой трансляции. Метка делает имена уникальными.
	run := strconv.FormatInt(time.Now().Unix(), 36)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(runCtx, h.cfg.Binary, h.args(cam, dir, playlist, run)...)
	// Своя группа процессов: ffmpeg порождает потомков, и убивать
	// нужно всю группу, иначе останутся висеть читатели RTSP.
	setGroup(cmd)
	cmd.Cancel = func() error { return stopGroup(cmd, false) }
	cmd.WaitDelay = 5 * time.Second

	var errTail tail
	cmd.Stderr = &errTail
	cmd.Stdout = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск ffmpeg: %w", err)
	}

	// Наблюдение за плейлистом: по нему видно и выход в эфир, и
	// замирание потока, которое сам ffmpeg молча переживает.
	watchDone := make(chan error, 1)
	go func() { watchDone <- h.watch(runCtx, playlist, onLive) }()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case err := <-waitErr:
		cancel()
		<-watchDone
		if ctx.Err() != nil {
			return nil // штатная остановка сервиса
		}
		// Наружу — короткая причина: её читает дежурный. Подробности
		// ffmpeg нужны инженеру и остаются в журнале.
		detail := errTail.String()
		h.log.Error("ffmpeg завершился", "camera", cam.ID, "err", err, "вывод", detail)
		return errors.New(reason(detail))

	case err := <-watchDone:
		cancel()
		<-waitErr
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

// args собирает командную строку ffmpeg.
func (h *HLS) args(cam video.Camera, dir, playlist, run string) []string {
	micros := strconv.FormatInt(h.cfg.Timeout.Microseconds(), 10)

	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "warning",
	}

	// Параметры входа. Для RTSP важны транспорт и предел ожидания:
	// без него ffmpeg висит на пропавшей камере бесконечно.
	if strings.HasPrefix(cam.URL, "rtsp") {
		args = append(args,
			"-rtsp_transport", string(cam.Transport),
			"-timeout", micros,
			"-fflags", "nobuffer",
		)
	}
	args = append(args, "-i", cam.URL)

	// Перекладываем видео как есть. Звук — только если он нужен:
	// лишняя дорожка увеличивает трафик, а на объекте он не бесплатный.
	args = append(args, "-c:v", "copy")
	if cam.Audio {
		args = append(args, "-c:a", "aac", "-b:a", "64k")
	} else {
		args = append(args, "-an")
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.FormatFloat(h.cfg.Segment.Seconds(), 'f', 1, 64),
		"-hls_list_size", strconv.Itoa(h.cfg.Window),
		// delete_segments — чтобы диск не кончился за сутки;
		// omit_endlist — чтобы плеер не считал эфир завершённым.
		"-hls_flags", "delete_segments+append_list+omit_endlist+program_date_time",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(dir, "seg_"+run+"_%05d.ts"),
		playlist,
	)
	return args
}

// watch следит за плейлистом: сообщает о выходе в эфир и возвращает
// ошибку, если файл перестал обновляться.
func (h *HLS) watch(ctx context.Context, playlist string, onLive func()) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var live bool
	lastChange := time.Now()

	// Следим за временем изменения, а не за размером: у живого потока
	// плейлист переписывается каждые несколько секунд, но его размер
	// остаётся прежним — окно сегментов постоянно, имена одной длины.
	// Проверка по размеру объявляла бы исправную камеру замершей.
	var lastWrite time.Time

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		info, err := os.Stat(playlist)
		switch {
		case err == nil && info.ModTime().After(lastWrite):
			lastWrite = info.ModTime()
			lastChange = time.Now()
			if !live {
				live = true
				if onLive != nil {
					onLive()
				}
			}

		case time.Since(lastChange) > h.cfg.Stall:
			if !live {
				return fmt.Errorf("камера не вышла в эфир за %s", h.cfg.Stall)
			}
			return fmt.Errorf("поток замер: плейлист не обновлялся %s",
				time.Since(lastChange).Round(time.Second))
		}
	}
}

// clean удаляет сегменты и плейлист прошлого запуска.
func clean(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".m3u8") {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// tail хранит последние строки stderr ffmpeg: в сообщении об ошибке
// нужна причина, а не весь поток предупреждений за часы работы.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailLimit = 2048

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if len(t.buf) > tailLimit {
		t.buf = t.buf[len(t.buf)-tailLimit:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return strings.TrimSpace(string(t.buf))
}

// reason переводит ругань ffmpeg в одну фразу для дежурного.
//
// Дежурному нужно знать, что делать: идти к камере, звать сетевиков
// или проверять пароль. Стек адресов и указателей ему в этом не помогает.
func reason(stderr string) string {
	low := strings.ToLower(stderr)

	switch {
	case strings.Contains(low, "connection refused"):
		return "камера не отвечает: соединение отклонено"
	case strings.Contains(low, "no route to host"), strings.Contains(low, "network is unreachable"):
		return "камера недоступна по сети"
	case strings.Contains(low, "timed out"), strings.Contains(low, "timeout"):
		return "камера не ответила вовремя"
	case strings.Contains(low, "401"), strings.Contains(low, "unauthorized"):
		return "камера отклонила логин или пароль"
	case strings.Contains(low, "404"), strings.Contains(low, "not found"):
		return "на камере нет такого потока"
	case strings.Contains(low, "invalid data found"):
		return "поток с камеры не удалось разобрать"
	case strings.Contains(low, "server returned 5"):
		return "камера ответила ошибкой"
	default:
		return "видеоконвейер остановился, подробности в журнале сервиса"
	}
}
