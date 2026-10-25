package video

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
)

type stubRepo struct{ cams []video.Camera }

func (s stubRepo) Cameras(context.Context) ([]video.Camera, error) { return s.cams, nil }

// flakyPublisher падает заданное число раз, потом выходит в эфир.
type flakyPublisher struct {
	mu      sync.Mutex
	fails   int
	runs    int
	liveNow chan struct{}
}

func (p *flakyPublisher) Run(ctx context.Context, _ video.Camera, onLive func()) error {
	p.mu.Lock()
	p.runs++
	fail := p.fails > 0
	if fail {
		p.fails--
	}
	p.mu.Unlock()

	if fail {
		return errors.New("камера не отвечает")
	}

	onLive()
	select {
	case p.liveNow <- struct{}{}:
	default:
	}

	<-ctx.Done()
	return nil
}

func (p *flakyPublisher) PlaylistURL(id video.CameraID) string {
	return "/video/" + string(id) + "/index.m3u8"
}

func cam(id string, enabled bool) video.Camera {
	return video.Camera{
		ID:        video.CameraID(id),
		URL:       "rtsp://admin:pass@192.168.1.64:554/Streaming/channels/101",
		Transport: video.TransportTCP,
		Enabled:   enabled,
	}
}

func TestSupervisorRestartsUntilLive(t *testing.T) {
	t.Parallel()

	pub := &flakyPublisher{fails: 2, liveNow: make(chan struct{}, 1)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	sup := New(stubRepo{cams: []video.Camera{cam("cam-1", true)}}, pub, log,
		Options{MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sup.Run(ctx)
	}()

	select {
	case <-pub.liveNow:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("камера так и не вышла в эфир")
	}

	st, ok := sup.State("cam-1")
	if !ok {
		t.Fatal("состояние камеры не заведено")
	}
	if !st.Live() {
		t.Errorf("статус = %q, ожидался live", st.Status)
	}
	if st.Restarts != 2 {
		t.Errorf("перезапусков = %d, ожидалось 2", st.Restarts)
	}
	if st.LastError == "" {
		t.Error("причина прошлого срыва должна сохраняться")
	}
	if got := sup.PlaylistURL("cam-1"); got != "/video/cam-1/index.m3u8" {
		t.Errorf("PlaylistURL() = %q", got)
	}

	// Остановка по контексту: все конвейеры должны свернуться.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("надзор не остановился по сигналу")
	}

	if st, _ := sup.State("cam-1"); st.Status != video.StatusStopped {
		t.Errorf("после остановки статус = %q, ожидался stopped", st.Status)
	}
}

func TestSupervisorSkipsDisabled(t *testing.T) {
	t.Parallel()

	pub := &flakyPublisher{liveNow: make(chan struct{}, 1)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	sup := New(stubRepo{cams: []video.Camera{cam("cam-off", false)}}, pub, log, Options{})

	if err := sup.Run(context.Background()); err != nil {
		t.Fatalf("Run() вернул ошибку: %v", err)
	}

	states := sup.States()
	if len(states) != 1 || states[0].Status != video.StatusDisabled {
		t.Fatalf("состояние выключенной камеры = %+v", states)
	}
	if pub.runs != 0 {
		t.Errorf("выключенную камеру запускали %d раз", pub.runs)
	}
}
