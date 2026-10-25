// Package config собирает настройки сервиса из флагов и переменных
// окружения. Значение флага перекрывает переменную окружения.
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen   string        // адрес UDP-приёмника; пусто — не слушать
	HTTPAddr string        // адрес HTTP API; пусто — не поднимать
	CSVPath  string        // файл CSV; пусто — не писать
	MongoURI string        // адрес MongoDB; пусто — работать без истории
	MongoDB  string        // имя базы
	Site     string        // объект по умолчанию для команд
	Silence  time.Duration // пауза, после которой источник считается пропавшим
	Buffer   int           // ёмкость очереди пакетов
	Quiet    bool          // не печатать каждый пакет

	// Видеонаблюдение.
	RoomsPath   string        // YAML с раскладкой объекта: комнаты, тренажёры, камеры
	CamerasPath string        // YAML с плоским списком камер (без комнат)
	VideoRoot   string        // каталог для сегментов HLS
	VideoPrefix string        // путь, под которым раздаются трансляции
	Segment     time.Duration // длительность сегмента HLS
	VideoWindow int           // сколько сегментов держать в плейлисте
	FFmpeg      string        // путь к ffmpeg
	PanelRoot   string        // каталог собранной панели; пусто — не раздавать
	RecordRoot  string        // каталог видеозаписей занятий; пусто — не писать
	// SoundsDir — каталог записей: фоновые эффекты и оповещения.
	// Звук живёт в центре, сборщики о нём не знают.
	SoundsDir string
	// StatePath — файл, в котором центр хранит занятия, картотеку,
	// состояния комнат и журнал аварий, когда базы нет.
	//
	// Нужен настольной сборке: там центр ставят одним файлом, и
	// поднимать рядом отдельную базу неуместно. Пусто — хранить
	// только в памяти, как было.
	StatePath string
	// SoundPlayer — чем проигрывать: программа и аргументы.
	// Пусто — ffmpeg прямо в ALSA.
	SoundPlayer []string
	RecordHours int // предел длительности одной записи, часы

	// Файл с учётными записями операторов тренажёра.
	// Пусто — вход не настроен, API открыт (отладка внутри контура).
	UsersPath string
}

// Load разбирает аргументы командной строки. args — без имени программы.
func Load(args []string) (Config, error) {
	cfg := Config{
		Listen:      env("MCHS_LISTEN", ":5005"),
		HTTPAddr:    env("MCHS_HTTP_ADDR", ":8080"),
		CSVPath:     env("MCHS_CSV", ""),
		MongoURI:    env("MCHS_MONGO_URI", ""),
		MongoDB:     env("MCHS_MONGO_DB", "mchs"),
		Site:        env("MCHS_SITE", "default"),
		Silence:     envDuration("MCHS_SILENCE", 3*time.Second),
		Buffer:      envInt("MCHS_BUFFER", 256),
		Quiet:       envBool("MCHS_QUIET", false),
		UsersPath:   env("MCHS_USERS", ""),
		RoomsPath:   env("MCHS_ROOMS", ""),
		CamerasPath: env("MCHS_CAMERAS", ""),
		// Каталог сегментов по умолчанию — во временном каталоге
		// системы: на Windows пути вида /tmp не существует.
		VideoRoot:   env("MCHS_VIDEO_ROOT", filepath.Join(os.TempDir(), "mchs-video")),
		VideoPrefix: env("MCHS_VIDEO_PREFIX", "/video"),
		Segment:     envDuration("MCHS_VIDEO_SEGMENT", 2*time.Second),
		VideoWindow: envInt("MCHS_VIDEO_WINDOW", 6),
		FFmpeg:      env("MCHS_FFMPEG", "ffmpeg"),
		PanelRoot:   env("MCHS_PANEL", ""),
		RecordRoot:  env("MCHS_RECORDS", ""),
		// Звук объекта: каталог с записями. Пусто — звука нет, и
		// центр о нём молчит.
		SoundsDir: env("MCHS_SOUNDS", ""),
		StatePath: env("MCHS_STATE", ""),
		// Чем проигрывать. Умолчание — ALSA напрямую: на объекте
		// звуковая карта свободна. Там, где её держит звуковой
		// сервер, путь задаётся этой переменной.
		SoundPlayer: envList("MCHS_SOUND_PLAYER", nil),
		RecordHours: envInt("MCHS_RECORD_HOURS", 4),
	}

	fs := flag.NewFlagSet("telemetryd", flag.ContinueOnError)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "адрес UDP-приёмника (пусто — не слушать)")
	fs.StringVar(&cfg.HTTPAddr, "http", cfg.HTTPAddr, "адрес HTTP API (пусто — не поднимать)")
	fs.StringVar(&cfg.CSVPath, "csv", cfg.CSVPath, "файл для записи телеметрии в CSV")
	fs.StringVar(&cfg.MongoURI, "mongo", cfg.MongoURI, "адрес MongoDB (пусто — без истории)")
	fs.StringVar(&cfg.MongoDB, "mongo-db", cfg.MongoDB, "имя базы MongoDB")
	fs.StringVar(&cfg.Site, "site", cfg.Site, "объект по умолчанию для команд")
	fs.DurationVar(&cfg.Silence, "silence", cfg.Silence, "пауза, после которой источник считается пропавшим")
	fs.IntVar(&cfg.Buffer, "buffer", cfg.Buffer, "ёмкость очереди принятых пакетов")
	fs.BoolVar(&cfg.Quiet, "quiet", cfg.Quiet, "не печатать каждый пакет, только события")
	fs.StringVar(&cfg.UsersPath, "users", cfg.UsersPath, "файл учётных записей операторов")
	fs.StringVar(&cfg.RoomsPath, "rooms", cfg.RoomsPath, "YAML с раскладкой объекта: комнаты, тренажёры, камеры")
	fs.StringVar(&cfg.CamerasPath, "cameras", cfg.CamerasPath, "YAML с плоским списком камер (без комнат)")
	fs.StringVar(&cfg.VideoRoot, "video-root", cfg.VideoRoot, "каталог для сегментов трансляций")
	fs.StringVar(&cfg.VideoPrefix, "video-prefix", cfg.VideoPrefix, "путь, под которым раздаются трансляции")
	fs.DurationVar(&cfg.Segment, "video-segment", cfg.Segment, "длительность сегмента HLS")
	fs.IntVar(&cfg.VideoWindow, "video-window", cfg.VideoWindow, "сколько сегментов держать в плейлисте")
	fs.StringVar(&cfg.FFmpeg, "ffmpeg", cfg.FFmpeg, "путь к ffmpeg")
	fs.StringVar(&cfg.PanelRoot, "panel", cfg.PanelRoot, "каталог собранной панели (пусто — не раздавать)")
	fs.StringVar(&cfg.RecordRoot, "records", cfg.RecordRoot, "каталог видеозаписей занятий (пусто — не писать)")
	fs.IntVar(&cfg.RecordHours, "record-hours", cfg.RecordHours, "предел длительности одной видеозаписи, часы")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	cfg.fillFromExeDir()
	return cfg, cfg.Validate()
}

// fillFromExeDir подставляет файлы, лежащие рядом с исполняемым
// файлом, если путь к ним не задан.
//
// Нужно там, где сервис запускают двойным щелчком: переменные
// окружения из батника при этом не действуют, и центр поднимался бы
// без учётных записей и без раскладки, молча пуская всех подряд.
//
// Подставляется только то, что действительно лежит рядом: в
// контейнере и на сервере рядом с бинарём ничего нет, и поведение там
// не меняется.
func (c *Config) fillFromExeDir() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	c.fillFromExeDirAt(filepath.Dir(exe))
}

// fillFromExeDirAt — то же, но для заданного каталога: так его можно
// проверить тестом, не подменяя исполняемый файл.
func (c *Config) fillFromExeDirAt(dir string) {
	byDefault := func(dst *string, name string, wantDir bool) {
		if *dst != "" {
			return
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() != wantDir {
			return
		}
		*dst = path
	}

	// ffmpeg ищется в PATH, а рядом с центром его туда никто не
	// добавляет: на Windows его просто кладут в ту же папку. Если
	// путь не задавали руками, берём соседний файл.
	if c.FFmpeg == "ffmpeg" {
		for _, name := range []string{"ffmpeg.exe", "ffmpeg"} {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				c.FFmpeg = path
				break
			}
		}
	}

	byDefault(&c.UsersPath, "users.json", false)
	byDefault(&c.RoomsPath, "rooms.yaml", false)
	byDefault(&c.SoundsDir, "sounds", true)

	// Список камер — только когда раскладки нет: камеры описываются в
	// одном месте, и два источника сразу центр не примет.
	if c.RoomsPath == "" {
		byDefault(&c.CamerasPath, "cameras.yaml", false)
	}
}

func (c Config) Validate() error {
	// Без обоих входов сервис бесполезен: принимать телеметрию нечем.
	if c.Listen == "" && c.HTTPAddr == "" {
		return fmt.Errorf("не задан ни UDP-приёмник, ни HTTP API — принимать телеметрию нечем")
	}
	if c.Buffer <= 0 {
		return fmt.Errorf("ёмкость очереди должна быть положительной, получено %d", c.Buffer)
	}
	if c.Silence < 0 {
		return fmt.Errorf("порог тишины не может быть отрицательным")
	}
	if c.RoomsPath != "" && c.CamerasPath != "" {
		return fmt.Errorf("заданы и раскладка объекта (-rooms), и список камер (-cameras): камеры описываются в одном месте")
	}
	if c.VideoSource() != "" {
		if c.VideoRoot == "" {
			return fmt.Errorf("камеры заданы, но не указан каталог для сегментов (-video-root)")
		}
		if c.HTTPAddr == "" {
			return fmt.Errorf("камеры заданы, но HTTP API выключен — трансляции некому отдавать")
		}
		if c.Segment <= 0 {
			return fmt.Errorf("длительность сегмента должна быть положительной")
		}
		if c.VideoWindow <= 0 {
			return fmt.Errorf("плейлист должен содержать хотя бы один сегмент")
		}
	}
	return nil
}

// VideoSource — файл, из которого берутся камеры: раскладка объекта
// или плоский список. Пусто — видеонаблюдение не поднимается.
func (c Config) VideoSource() string {
	if c.RoomsPath != "" {
		return c.RoomsPath
	}
	return c.CamerasPath
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}

// envList читает список, разделённый пробелами: так переменная
// окружения задаёт командную строку проигрывателя.
func envList(key string, def []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	return strings.Fields(raw)
}
