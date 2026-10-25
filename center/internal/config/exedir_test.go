package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Центр, запущенный двойным щелчком, должен найти свои файлы сам.
//
// Переменные окружения из батника при этом не действуют, и без этой
// подстановки сервис поднимался бы без учётных записей — то есть
// пускал бы в панель кого угодно.
func TestФайлыРядомСИсполняемымПодставляются(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mchs-center.exe")
	if err := os.WriteFile(exe, []byte("не настоящий"), 0o755); err != nil {
		t.Fatalf("подготовка: %v", err)
	}
	for _, name := range []string{"users.json", "rooms.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("подготовка: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sounds"), 0o755); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	cfg := Config{}
	cfg.fillFromExeDirAt(dir)

	if cfg.UsersPath != filepath.Join(dir, "users.json") {
		t.Errorf("учётные записи не найдены: %q", cfg.UsersPath)
	}
	if cfg.RoomsPath != filepath.Join(dir, "rooms.yaml") {
		t.Errorf("раскладка не найдена: %q", cfg.RoomsPath)
	}
	if cfg.SoundsDir != filepath.Join(dir, "sounds") {
		t.Errorf("каталог звуков не найден: %q", cfg.SoundsDir)
	}
}

// ffmpeg, положенный рядом с центром, находится без PATH: на Windows
// его кладут в ту же папку, и добавлять его в PATH никто не станет.
func TestСоседнийFfmpegНаходится(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ffmpeg.exe")
	if err := os.WriteFile(exe, []byte("не настоящий"), 0o755); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	cfg := Config{FFmpeg: "ffmpeg"}
	cfg.fillFromExeDirAt(dir)

	if cfg.FFmpeg != exe {
		t.Errorf("соседний ffmpeg не найден: %q", cfg.FFmpeg)
	}
}

// Заданный путь к ffmpeg не перебивается соседним файлом.
func TestЗаданныйFfmpegНеПеребивается(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg.exe"), []byte("x"), 0o755); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	cfg := Config{FFmpeg: "C:\\tools\\ffmpeg.exe"}
	cfg.fillFromExeDirAt(dir)

	if cfg.FFmpeg != "C:\\tools\\ffmpeg.exe" {
		t.Errorf("заданный путь подменён: %q", cfg.FFmpeg)
	}
}

// Заданное руками не перебивается: на сервере файлы лежат не рядом с
// бинарём, и подстановка не должна уводить центр на чужую раскладку.
func TestЗаданныеПутиНеПеребиваются(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"users.json", "rooms.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("подготовка: %v", err)
		}
	}

	cfg := Config{UsersPath: "/etc/mchs/users.json", RoomsPath: "/etc/mchs/rooms.yaml"}
	cfg.fillFromExeDirAt(dir)

	if cfg.UsersPath != "/etc/mchs/users.json" || cfg.RoomsPath != "/etc/mchs/rooms.yaml" {
		t.Errorf("заданные пути подменены: %+v", cfg)
	}
}

// Пустой каталог ничего не подставляет: в контейнере рядом с бинарём
// ничего нет, и поведение там прежнее.
func TestПустойКаталогНичегоНеМеняет(t *testing.T) {
	cfg := Config{}
	cfg.fillFromExeDirAt(t.TempDir())

	if cfg.UsersPath != "" || cfg.RoomsPath != "" || cfg.SoundsDir != "" {
		t.Errorf("подставлено из пустого каталога: %+v", cfg)
	}
}
