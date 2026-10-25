package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

type fakeRepo struct {
	mu    sync.Mutex
	saved []telemetry.Packet
	err   error
}

func (f *fakeRepo) Save(_ context.Context, p telemetry.Packet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, p)
	return f.err
}

func (f *fakeRepo) Latest(context.Context, telemetry.DeviceID) (telemetry.Packet, bool, error) {
	return telemetry.Packet{}, false, nil
}

func (f *fakeRepo) List(context.Context) ([]telemetry.Packet, error) { return nil, nil }

func (f *fakeRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

type fakeSink struct {
	mu   sync.Mutex
	got  []telemetry.Packet
	fail error
}

func (f *fakeSink) Consume(_ context.Context, p telemetry.Packet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, p)
	return f.fail
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

type fakeSource struct{ packets []telemetry.Packet }

func (f fakeSource) Packets(context.Context) (<-chan telemetry.Packet, error) {
	ch := make(chan telemetry.Packet, len(f.packets))
	for _, p := range f.packets {
		ch <- p
	}
	close(ch)
	return ch, nil
}

type fakeBcast struct {
	mu   sync.Mutex
	sent int
}

func (f *fakeBcast) Publish(telemetry.Packet) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func packet(id string, t telemetry.Telemetry) telemetry.Packet {
	return telemetry.Packet{Device: telemetry.DeviceID(id), Received: time.Now(), Data: t}
}

func TestНегодныйПакетНеДоходитДоХранилища(t *testing.T) {
	repo, sk := &fakeRepo{}, &fakeSink{}
	svc := New(repo, []Sink{sk}, nil, quiet(), Options{})

	// Событие клавиши без самой клавиши нарушает инвариант модели.
	svc.Handle(context.Background(), packet("stm32-01", telemetry.Telemetry{Event: telemetry.EventKey}))

	if repo.count() != 0 || sk.count() != 0 {
		t.Fatal("негодный пакет просочился в тракт")
	}
}

// Один сбойный потребитель не должен рвать поток: остальные обязаны
// получить пакет, а приём — продолжиться.
func TestОтказПотребителяНеОстанавливаетПриём(t *testing.T) {
	repo := &fakeRepo{}
	bad := &fakeSink{fail: errors.New("диск переполнен")}
	good := &fakeSink{}

	svc := New(repo, []Sink{bad, good}, nil, quiet(), Options{})
	src := fakeSource{packets: []telemetry.Packet{
		packet("stm32-01", telemetry.Telemetry{RPM: 1500}),
		packet("stm32-01", telemetry.Telemetry{RPM: 1600}),
	}}

	if err := svc.Run(context.Background(), src); err != nil {
		t.Fatalf("приём остановлен: %v", err)
	}
	if good.count() != 2 {
		t.Errorf("исправный потребитель получил %d пакетов из 2", good.count())
	}
	if repo.count() != 2 {
		t.Errorf("в хранилище %d пакетов из 2", repo.count())
	}
}

// Ошибка хранилища тоже не повод терять поток.
func TestОшибкаХранилищаНеТеряетПоток(t *testing.T) {
	repo := &fakeRepo{err: errors.New("база недоступна")}
	sk := &fakeSink{}

	svc := New(repo, []Sink{sk}, nil, quiet(), Options{})
	svc.Handle(context.Background(), packet("stm32-01", telemetry.Telemetry{RPM: 1500}))

	if sk.count() != 1 {
		t.Error("потребитель не получил пакет при сбое хранилища")
	}
}

func TestПакетыУходятВРассылку(t *testing.T) {
	b := &fakeBcast{}
	svc := New(&fakeRepo{}, nil, b, quiet(), Options{})

	svc.Handle(context.Background(), packet("stm32-01", telemetry.Telemetry{RPM: 1500}))
	svc.Handle(context.Background(), packet("stm32-01", telemetry.Telemetry{Event: telemetry.EventKey, Key: "5"}))
	// Негодный пакет в рассылку попасть не должен.
	svc.Handle(context.Background(), packet("stm32-01", telemetry.Telemetry{Event: telemetry.EventRelay}))

	if b.sent != 2 {
		t.Errorf("разослано %d пакетов, ожидалось 2", b.sent)
	}
}

// Приём завершается по закрытию канала, а не по отмене контекста:
// пакеты, пришедшие до сигнала, должны быть обработаны.
func TestПриёмДообрабатываетОчередь(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(repo, nil, nil, quiet(), Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := fakeSource{packets: []telemetry.Packet{
		packet("stm32-01", telemetry.Telemetry{RPM: 1}),
		packet("stm32-01", telemetry.Telemetry{RPM: 2}),
		packet("stm32-01", telemetry.Telemetry{RPM: 3}),
	}}

	if err := svc.Run(ctx, src); err != nil {
		t.Fatalf("приём: %v", err)
	}
	if repo.count() != 3 {
		t.Errorf("обработано %d пакетов из 3 при отменённом контексте", repo.count())
	}
}
