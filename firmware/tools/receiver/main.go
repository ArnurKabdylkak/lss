// Приёмник UDP-телеметрии от STM32F767 (тахометр на датчике Холла).
//
// Слушает порт, на который плата шлёт JSON (NET_SERVER_PORT в include/net.h),
// печатает каждый пакет и при необходимости пишет их в CSV.
//
//	go run ./tools/receiver -listen :5005
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Максимальный размер датаграммы, которую шлёт плата, с запасом.
const maxDatagramSize = 1024

type config struct {
	listen  string
	csvPath string
	timeout time.Duration
	quiet   bool
}

func main() {
	var cfg config

	flag.StringVar(&cfg.listen, "listen", ":5005", "адрес и порт для прослушивания")
	flag.StringVar(&cfg.csvPath, "csv", "", "файл для записи телеметрии в CSV (по умолчанию не пишется)")
	flag.DurationVar(&cfg.timeout, "timeout", 3*time.Second, "предупреждать, если пакетов нет дольше этого времени")
	flag.BoolVar(&cfg.quiet, "quiet", false, "не печатать каждый пакет, только события")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("приёмник остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	conn, err := net.ListenPacket("udp", cfg.listen)
	if err != nil {
		return fmt.Errorf("открытие UDP-сокета: %w", err)
	}
	defer conn.Close()

	var writer *csvWriter
	if cfg.csvPath != "" {
		writer, err = newCSVWriter(cfg.csvPath)
		if err != nil {
			return fmt.Errorf("создание CSV: %w", err)
		}
		defer func() {
			if err := writer.Close(); err != nil {
				logger.Error("закрытие CSV", "err", err)
			}
		}()
	}

	logger.Info("слушаю телеметрию", "addr", conn.LocalAddr().String())

	// Отмена по сигналу разблокирует ReadFrom: закрываем сокет, и чтение
	// возвращает ошибку, которую ниже трактуем как штатное завершение.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	packets := make(chan Packet, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		consume(packets, writer, cfg, logger)
	}()

	buf := make([]byte, maxDatagramSize)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			// Закрываем канал и ждём, пока consume допишет хвост и подведёт итог.
			close(packets)
			<-done

			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				logger.Info("завершение по сигналу")
				return nil
			}
			return fmt.Errorf("чтение датаграммы: %w", err)
		}

		data, err := parseTelemetry(buf[:n])
		if err != nil {
			logger.Warn("пакет не разобран", "from", addr.String(), "raw", string(buf[:n]), "err", err)
			continue
		}

		from, _ := netip.ParseAddrPort(addr.String())
		packets <- Packet{Received: time.Now(), From: from, Data: data}
	}
}

// consume печатает пакеты, пишет CSV и следит за паузами в потоке данных.
// Завершается по закрытию канала — так гарантированно обрабатывается
// всё, что успело прийти до сигнала.
func consume(packets <-chan Packet, writer *csvWriter,
	cfg config, logger *slog.Logger) {

	ticker := time.NewTicker(cfg.timeout / 2)
	defer ticker.Stop()

	var (
		last       time.Time
		lastUptime uint32
		count      uint64
		warned     bool
	)

	for {
		select {
		case p, ok := <-packets:
			if !ok {
				logger.Info("итог", "packets", count)
				return
			}

			// Счётчик uptime на плате идёт только вверх; его падение
			// означает, что контроллер перезапустился.
			if count > 0 && p.Data.UptimeMS < lastUptime {
				logger.Warn("плата перезапустилась", "uptime_ms", p.Data.UptimeMS)
			}

			if warned {
				logger.Info("связь восстановлена", "pause", time.Since(last).Round(time.Millisecond))
				warned = false
			}

			last = p.Received
			lastUptime = p.Data.UptimeMS
			count++

			if !cfg.quiet {
				fmt.Println(p)
			}

			if writer != nil {
				if err := writer.Write(p); err != nil {
					logger.Error("запись в CSV", "err", err)
				}
			}

		case <-ticker.C:
			if count == 0 || warned {
				continue
			}
			if pause := time.Since(last); pause > cfg.timeout {
				logger.Warn("телеметрия пропала", "pause", pause.Round(time.Millisecond))
				warned = true
			}
		}
	}
}
