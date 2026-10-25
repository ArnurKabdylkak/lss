// Команда telemetryd — центр сбора телеметрии тренажёрного комплекса.
//
// Служебный вход: запускается системой, живёт до сигнала остановки,
// говорит журналом. Десктопное приложение для Windows собирает тот же
// центр, но вокруг него окно с панелью — см. cmd/mchs-desktop.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ArnurKabdylkak/lss/center/internal/app/center"
	"github.com/ArnurKabdylkak/lss/center/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		logger.Error("конфигурация", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := center.Run(ctx, cfg, logger); err != nil {
		logger.Error("сервис остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}
