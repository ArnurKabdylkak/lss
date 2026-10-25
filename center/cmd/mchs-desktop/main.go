//go:build windows

// Команда mchs-desktop — центр тренажёрного комплекса как обычное
// приложение Windows.
//
// Один файл, двойной щелчок, окно с панелью. Внутри тот же центр, что
// работает службой на сервере: приём телеметрии от сборщиков, видео,
// звук, журнал занятий. Разница только в оболочке — здесь у него окно,
// а не консоль.
//
// Почему окно, а не браузер: оператор работает в зале, и приложение
// должно вести себя как приложение — открываться с рабочего стола,
// закрываться крестиком, не требовать, чтобы кто-то помнил адрес и не
// закрывал «ту самую вкладку». Браузер при этом никуда не девается:
// с других машин объекта панель открывается по адресу этой.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jchv/go-webview2"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/deps"
	"github.com/ArnurKabdylkak/lss/center/internal/app/center"
	"github.com/ArnurKabdylkak/lss/center/internal/config"
)

func main() {
	logger, logFile := openLog()
	if logFile != nil {
		defer logFile.Close()
	}

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		fail(logger, "Не удалось прочитать настройки", err)
		return
	}

	// Всё нужное лежит внутри программы и раскладывается при первом
	// запуске: на объекте нет ни сети наружу, ни желания ставить
	// что-то руками.
	if err := unpackDeps(&cfg, logger); err != nil {
		fail(logger, "Не удалось подготовить вложенные файлы", err)
		return
	}

	// Хранилище файлом: занятия, картотека и журнал аварий переживают
	// перезапуск без отдельной базы.
	if cfg.StatePath == "" && cfg.MongoURI == "" {
		cfg.StatePath = filepath.Join(exeDir(), "data", "state.json")
	}
	// Видеозаписи и сегменты трансляций — рядом с программой, а не во
	// временном каталоге системы: их приобщают к карточкам, и терять
	// их при очистке диска нельзя.
	if cfg.RecordRoot == "" {
		cfg.RecordRoot = filepath.Join(exeDir(), "records")
	}

	// Порт берётся из настроек, но если он занят — приложение не
	// должно молча не открыться. Проверяем заранее и говорим об этом
	// человеку, а не в журнал, который он не увидит.
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":8080"
		cfg.HTTPAddr = addr
	}
	if err := checkPort(addr); err != nil {
		fail(logger, "Порт "+addr+" занят другой программой", err)
		return
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// Центр поднимается в своей горутине: окно должно появиться сразу,
	// не дожидаясь, пока поднимутся камеры и база.
	done := make(chan error, 1)
	go func() { done <- center.Run(ctx, cfg, logger) }()

	// Ждём, пока центр ответит: окно, открытое раньше, показало бы
	// ошибку соединения и напугало оператора на пустом месте.
	url := localURL(addr)
	if err := waitReady(ctx, url, done, 20*time.Second); err != nil {
		fail(logger, "Центр не запустился", err)
		stop()
		return
	}

	// Проверяем компонент до обращения к нему: без него вызов роняет
	// программу молча, и оператор видит лишь то, что она «не
	// открывается».
	if err := ensureWebView(logger); err != nil {
		fail(logger, "Нет компонента для показа окна", err)
		stop()
		return
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		// Данные движка — рядом с приложением, а не в профиле
		// пользователя: на объекте за одним компьютером работают
		// разные смены под одной учётной записью.
		DataPath: filepath.Join(exeDir(), "webview"),
		WindowOptions: webview2.WindowOptions{
			Title:  "Тренажёрный комплекс",
			Width:  1440,
			Height: 900,
			Center: true,
		},
	})
	if w == nil {
		fail(logger, "Не удалось открыть окно",
			fmt.Errorf("нет компонента WebView2 — см. README.md"))
		stop()
		return
	}
	defer w.Destroy()

	w.Navigate(url)
	logger.Info("окно открыто", "адрес", url)

	// Run держит окно до закрытия крестиком.
	w.Run()

	// Окно закрыли — гасим центр и даём ему дописать видеозаписи.
	logger.Info("окно закрыто, останавливаю центр")
	stop()
	select {
	case err := <-done:
		if err != nil {
			logger.Error("центр остановлен с ошибкой", "err", err)
		}
	case <-time.After(15 * time.Second):
		logger.Error("центр не остановился вовремя")
	}
}

// waitReady ждёт, пока центр начнёт отвечать.
func waitReady(ctx context.Context, url string, done <-chan error,
	limit time.Duration) error {

	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: time.Second}

	for {
		// Центр мог упасть на старте — тогда ждать нечего, и причина
		// уже известна.
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			return fmt.Errorf("центр завершился сразу после запуска")
		default:
		}

		resp, err := client.Get(url + "healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("центр не ответил за %s", limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// checkPort проверяет, свободен ли адрес.
func checkPort(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

// localURL собирает адрес панели для окна.
func localURL(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "http://127.0.0.1:8080/"
	}
	return "http://127.0.0.1:" + port + "/"
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// openLog заводит журнал файлом рядом с приложением.
//
// У оконного приложения нет консоли: без файла единственным следом
// неполадки осталось бы само окно, которое не открылось.
func openLog() (*slog.Logger, *os.File) {
	path := filepath.Join(exeDir(), "mchs-center.log")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return slog.New(slog.NewTextHandler(os.Stderr,
			&slog.HandlerOptions{Level: slog.LevelInfo})), nil
	}
	return slog.New(slog.NewTextHandler(f,
		&slog.HandlerOptions{Level: slog.LevelInfo})), f
}

// unpackDeps раскладывает вложенные файлы и указывает на них центру.
func unpackDeps(cfg *config.Config, log *slog.Logger) error {
	file, ok := deps.FFmpeg()
	if !ok {
		// Сборка без вложений: ffmpeg берётся из системы, как на
		// сервере.
		return nil
	}

	// Рядом с программой, в своей папке: каталог с программой у
	// оператора на виду, и лишние файлы в нём сбивают с толку.
	u := deps.New(filepath.Join(exeDir(), "bin"), log)
	path, err := u.Ensure(file)
	if err != nil {
		return err
	}

	// Путь подставляется, только если его не задали руками: своя
	// сборка ffmpeg на объекте — законный случай.
	if cfg.FFmpeg == "" || cfg.FFmpeg == "ffmpeg" {
		cfg.FFmpeg = path
	}
	return nil
}
