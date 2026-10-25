// Команда сборщика телеметрии на объекте.
//
// Принимает UDP от плат, присваивает им идентичность, копит данные на
// диске и выгружает их в центр пачками. Обрыв канала наверх не
// останавливает сбор.
//
//	go run ./cmd -config config.yml
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ArnurKabdylkak/lss/collector/internal/app"
)

func main() {
	var configPath string
	var checkOnly bool
	for i, arg := range os.Args[1:] {
		switch arg {
		case "-config", "--config":
			if i+2 <= len(os.Args)-1 {
				configPath = os.Args[i+2]
			}
		case "-check", "--check":
			// Проверка настройки без запуска: нужна при выкладке,
			// чтобы негодный файл не доехал до объекта и не уронил
			// сборщик уже на месте.
			checkOnly = true
		}
	}
	if configPath == "" {
		configPath = envOr("MCHS_CONFIG", "config.yml")
	}

	level := slog.LevelInfo
	if os.Getenv("MCHS_DEBUG") != "" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := app.Load(configPath)
	if err != nil {
		logger.Error("конфигурация", "path", configPath, "err", err)
		os.Exit(2)
	}

	if checkOnly {
		logger.Info("настройка в порядке",
			"файл", configPath,
			"объект", cfg.Site,
			"сборщик", cfg.CollectorID(),
			"центр", cfg.Center.URL,
			"плат", len(cfg.Devices),
			"выходов", len(cfg.Outputs.Lines),
			"узлов", len(cfg.Room.Units))
		return
	}

	logger.Info("сборщик запускается", "site", cfg.Site, "сборщик", cfg.CollectorID(),
		"listen", cfg.Listen, "center", cfg.Center.URL, "devices", len(cfg.Devices))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, logger); err != nil {
		logger.Error("сборщик остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
