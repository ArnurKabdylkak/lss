// Package udp — входной адаптер: слушает UDP-порт, декодирует датаграммы
// и отдаёт пакеты прикладному слою.
//
// В трёхзвенной схеме основной вход центра — батчи от сборщика; этот
// адаптер остаётся для отладки и для объектов без сборщика.
package udp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// MaxDatagramSize — потолок размера датаграммы с запасом относительно
// того, что шлёт плата.
const MaxDatagramSize = 1024

// Config — параметры приёмника.
type Config struct {
	Listen string        // адрес и порт, например ":5005"
	Buffer int           // ёмкость канала пакетов
	Read   time.Duration // таймаут чтения; ноль — без таймаута
}

// Server реализует port.PacketSource поверх UDP.
type Server struct {
	cfg Config
	log *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Server {
	if cfg.Buffer <= 0 {
		cfg.Buffer = 64
	}
	return &Server{cfg: cfg, log: log}
}

// Packets открывает сокет и запускает чтение в отдельной горутине.
// Канал закрывается, когда приём остановлен.
func (s *Server) Packets(ctx context.Context) (<-chan telemetry.Packet, error) {
	conn, err := net.ListenPacket("udp", s.cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("открытие UDP-сокета: %w", err)
	}

	s.log.Info("слушаю телеметрию", "addr", conn.LocalAddr().String())

	// Отмена контекста разблокирует ReadFrom: закрываем сокет, и чтение
	// возвращает net.ErrClosed, которую трактуем как штатное завершение.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	out := make(chan telemetry.Packet, s.cfg.Buffer)
	go func() {
		defer close(out)
		defer conn.Close()
		s.loop(ctx, conn, out)
	}()

	return out, nil
}

func (s *Server) loop(ctx context.Context, conn net.PacketConn, out chan<- telemetry.Packet) {
	buf := make([]byte, MaxDatagramSize)

	for {
		if s.cfg.Read > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.cfg.Read))
		}

		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				s.log.Info("приём остановлен")
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			s.log.Error("чтение датаграммы", "err", err)
			return
		}

		data, err := telemetry.DecodeDatagram(buf[:n])
		if err != nil {
			s.log.Warn("пакет не разобран", "from", addr.String(),
				"raw", string(buf[:n]), "err", err)
			continue
		}

		from, err := netip.ParseAddrPort(addr.String())
		if err != nil {
			s.log.Warn("адрес источника не разобран", "addr", addr.String(), "err", err)
			continue
		}

		// Прямой приём без сборщика: опознавать плату больше нечем,
		// поэтому идентификатором остаётся адрес источника.
		p := telemetry.Packet{
			Device:   telemetry.DeviceID(from.Addr().String()),
			Received: time.Now(),
			From:     from,
			Data:     data,
		}

		select {
		case out <- p:
		case <-ctx.Done():
			return
		}
	}
}
