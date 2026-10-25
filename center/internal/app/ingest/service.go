// Package ingest реализует сценарий приёма телеметрии: пакет проверяется,
// сохраняется как последнее состояние устройства и раздаётся потребителям.
// Слой не знает ни про UDP, ни про формат датаграммы.
package ingest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Sink — потребитель принятых пакетов (см. port.Sink).
type Sink = port.Sink

// Options настраивает сценарий приёма.
type Options struct {
	// Silence — пауза в потоке, после которой источник считается
	// пропавшим. Ноль отключает наблюдение.
	//
	// В трёхзвенной схеме молчание означает недоступность сборщика,
	// а не платы: живость плат отслеживает сборщик, который видит
	// настоящий поток (§9 плана).
	Silence time.Duration
}

// Service — прикладной сценарий приёма телеметрии.
type Service struct {
	repo  port.StateRepository
	sinks []port.Sink
	bcast port.Broadcaster
	log   *slog.Logger
	opts  Options

	mu    sync.Mutex
	state map[telemetry.DeviceID]*deviceState
}

type deviceState struct {
	last    telemetry.Packet
	count   uint64
	silent  bool
	present bool
}

func New(repo port.StateRepository, sinks []port.Sink, bcast port.Broadcaster,
	log *slog.Logger, opts Options) *Service {

	return &Service{
		repo:  repo,
		sinks: sinks,
		bcast: bcast,
		log:   log,
		opts:  opts,
		state: make(map[telemetry.DeviceID]*deviceState),
	}
}

// Run читает пакеты из источника, пока тот не закроет канал.
// Возвращает ошибку только если источник не удалось запустить.
func (s *Service) Run(ctx context.Context, src port.PacketSource) error {
	packets, err := src.Packets(ctx)
	if err != nil {
		return err
	}

	var ticker *time.Ticker
	var tick <-chan time.Time
	if s.opts.Silence > 0 {
		ticker = time.NewTicker(s.opts.Silence / 2)
		defer ticker.Stop()
		tick = ticker.C
	}

	for {
		select {
		case p, ok := <-packets:
			// Канал закрыт — транспорт остановлен, поток исчерпан.
			// Выходим только здесь, а не по ctx.Done(), чтобы дообработать
			// пакеты, успевшие прийти до сигнала.
			if !ok {
				s.summary()
				return nil
			}
			s.Handle(ctx, p)

		case <-tick:
			s.checkSilence()
		}
	}
}

// Handle проводит один пакет через тракт. Вызывается и приёмной петлёй,
// и входным адаптером батчей: сценарий приёма для них один.
func (s *Service) Handle(ctx context.Context, p telemetry.Packet) {
	if err := p.Data.Validate(); err != nil {
		s.log.Warn("пакет отброшен", "device", p.Device, "err", err)
		return
	}

	s.track(p)

	if err := s.repo.Save(ctx, p); err != nil {
		s.log.Error("сохранение состояния", "device", p.Device, "err", err)
	}

	for _, sink := range s.sinks {
		if err := sink.Consume(ctx, p); err != nil {
			s.log.Error("потребитель отказал", "err", err)
		}
	}

	if s.bcast != nil {
		s.bcast.Publish(p)
	}
}

// track ведёт статистику по устройству и сообщает о перезапуске платы
// и о восстановлении связи после паузы.
func (s *Service) track(p telemetry.Packet) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := p.Device
	st, ok := s.state[id]
	if !ok {
		st = &deviceState{}
		s.state[id] = st
		s.log.Info("устройство на связи", "device", id)
	}

	if st.present && p.Rebooted(st.last) {
		s.log.Warn("устройство перезапустилось", "device", id, "uptime_ms", p.Data.UptimeMS)
	}
	if st.silent {
		s.log.Info("связь восстановлена", "device", id,
			"pause", p.Received.Sub(st.last.Received).Round(time.Millisecond))
		st.silent = false
	}

	st.last = p
	st.present = true
	st.count++
}

func (s *Service) checkSilence() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, st := range s.state {
		if !st.present || st.silent {
			continue
		}
		if pause := time.Since(st.last.Received); pause > s.opts.Silence {
			s.log.Warn("телеметрия пропала", "device", id, "pause", pause.Round(time.Millisecond))
			st.silent = true
		}
	}
}

func (s *Service) summary() {
	s.mu.Lock()
	defer s.mu.Unlock()

	var total uint64
	for _, st := range s.state {
		total += st.count
	}
	s.log.Info("итог", "devices", len(s.state), "packets", total)
}
