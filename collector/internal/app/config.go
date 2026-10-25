// Package app содержит сборщик телеметрии на объекте.
//
// Сборщик стоит между платами и центром: принимает UDP от устройств,
// присваивает им идентичность, копит данные на диске и выгружает их
// пачками наверх. Обрыв канала до центра не останавливает сбор —
// ради этого звено и существует.
package app

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config — настройки сборщика (config.yml).
type Config struct {
	// Идентификатор объекта. Уходит в каждый батч.
	Site string `yaml:"site"`

	Listen string `yaml:"listen"` // UDP-приём от плат, например ":5005"
	HTTP   string `yaml:"http"`   // локальная панель и API, например ":8080"

	// Центр: куда выгружать и с какими параметрами.
	Center Center `yaml:"center"`

	// Буфер на диске.
	Buffer Buffer `yaml:"buffer"`

	// Карта устройств: адрес источника -> идентификатор.
	// Плата своего идентификатора не передаёт, опознать её больше нечем.
	Devices []DeviceMap `yaml:"devices"`

	// Принимать ли пакеты с незнакомых адресов.
	AcceptUnknown bool `yaml:"accept_unknown"`

	// Пауза, после которой устройство считается пропавшим.
	// Считается здесь: сборщик видит настоящий поток, центр — нет.
	Silence time.Duration `yaml:"silence"`

	// Собственные датчики сборщика по I2C.
	Sensors Sensors `yaml:"sensors"`

	// Реле на выводах RPi: тепловые пушки, стробоскоп, прочая нагрузка.
	Outputs OutputsConfig `yaml:"outputs"`

	// AlarmToken — общий секрет аварийной остановки с пульта.
	// Пусто — приём аварии напрямую выключен, и остановка ходит
	// только через центр.
	AlarmToken string `yaml:"alarm_token"`

	// Room — помещение, которым заведует этот сборщик: его узлы,
	// завалы, замки и звуковые сообщения. Команды помещения центр
	// адресует сборщику, а не отдельным реле: какое реле за что
	// отвечает, знает он.
	Room RoomConfig `yaml:"room"`
}

// RoomConfig — обвязка помещения.
//
// Центр не знает ни выводов, ни полярности: он говорит «включи дым»
// или «авария», а соответствие узлов выводам задаётся здесь.
type RoomConfig struct {
	// ID — идентификатор сборщика в центре: команды помещения
	// адресуются ему (obj-17-rpi-1). Пусто — берётся site.
	ID string `yaml:"id"`

	// Units — узлы помещения и выводы, которыми они включаются.
	// Чем они становятся при подготовке и по аварии, решает центр:
	// сценарий должен жить в одном месте.
	Units []UnitConfig `yaml:"units"`

	// Rubble — выходы механизма «Завалы». Отдельно от замков: их
	// магниты питаются независимой цепью, и аварийное открытие
	// дверей не должно ронять трубы.
	Rubble []string `yaml:"rubble"`

	// Locks — электромагнитные замки дверей: по аварии отпускаются.
	Locks []string `yaml:"locks"`
}

// UnitConfig — узел помещения.
type UnitConfig struct {
	// Unit — имя узла из контракта: smoke, flood, strobe, light,
	// fan, sound, heat.
	Unit string `yaml:"unit"`

	// Outputs — выходы, которыми узел включается. Их может быть
	// несколько: прожекторов в зале четыре, а узел один.
	Outputs []string `yaml:"outputs"`
}

// OutputsConfig — реле на выводах сборщика.
//
// Нагрузкой управляет сборщик, а не плата: реле висят на его выводах, и
// решение об отключении принимает тот, кто его исполняет.
//
// Обвязка на объекте одинаковая для всех модулей, поэтому полярность и
// режим вывода заданы здесь один раз, а у отдельного выхода их можно
// переопределить — платы разных партий бывают разными.
type OutputsConfig struct {
	Enabled bool `yaml:"enabled"`

	Chip      string `yaml:"chip"`       // /dev/gpiochip0
	ActiveLow bool   `yaml:"active_low"` // модуль реле с оптопарой
	// Открытый коллектор: выключенное состояние не выдаёт напряжение,
	// а отпускает линию. Нужен модулям реле с логикой на 5 В.
	OpenDrain bool `yaml:"open_drain"`

	Lines []OutputConfig `yaml:"lines"`
}

// OutputConfig — один выход.
type OutputConfig struct {
	// Короткое имя: в центре выход виден как <site>-<id>.
	ID string `yaml:"id"`

	Line uint32     `yaml:"line"` // номер вывода на чипе
	Kind OutputKind `yaml:"kind"` // heater или switch

	// Переопределение общей обвязки. Указатели, а не bool: нужно
	// отличать «задано false» от «не задано».
	ActiveLow *bool `yaml:"active_low"`
	OpenDrain *bool `yaml:"open_drain"`

	// Дальше — только для kind: heater. У ключа тепловой защиты нет:
	// стробоскопу греть нечего, и требовать от него температуру
	// значило бы запретить его включение без датчика.
	LimitC   float64 `yaml:"limit_c"`
	RestoreC float64 `yaml:"restore_c"`

	// Выдержка в отключённом состоянии. Ноль в файле означает «без
	// выдержки»; если поля нет вовсе — берётся умолчание.
	MinOff *time.Duration `yaml:"min_off"`

	// Свежесть показаний платы и порог расхождения датчиков.
	BoardTTL  time.Duration `yaml:"board_ttl"`
	DisagreeC float64       `yaml:"disagree_c"`

	// Какие устройства дают температуру для защиты. Пусто — все платы
	// объекта; обычно это одна плата рядом с пушкой.
	TempFrom []string `yaml:"temp_from"`
}

// Limits собирает уставки защиты для нагрева.
func (o OutputConfig) Limits() HeaterLimits {
	l := HeaterLimits{
		LimitC:    o.LimitC,
		RestoreC:  o.RestoreC,
		BoardTTL:  o.BoardTTL,
		DisagreeC: o.DisagreeC,
		MinOff:    30 * time.Second,
	}
	if o.MinOff != nil {
		l.MinOff = *o.MinOff
	}
	return l
}

// Polarity возвращает обвязку выхода: своё значение, если задано, иначе
// общее для объекта.
func (c OutputsConfig) Polarity(o OutputConfig) (activeLow, openDrain bool) {
	activeLow, openDrain = c.ActiveLow, c.OpenDrain
	if o.ActiveLow != nil {
		activeLow = *o.ActiveLow
	}
	if o.OpenDrain != nil {
		openDrain = *o.OpenDrain
	}
	return activeLow, openDrain
}

// OutputDeviceID — под каким идентификатором выход виден в центре.
//
// Имя берётся из конфигурации как есть, без приставки объекта. Так же
// задаются и платы в devices, и схема получается одна на все устройства:
// иначе в центре рядом оказались бы «r2-bike» и «obj-17-r2-heater».
//
// Объект у трёх сборщиков общий, поэтому уникальность на весь объект —
// забота имени, а не site: центр раскладывает устройства по комнатам
// именно по device_id (network-plan.xlsx, лист «Адресация»). Отсюда
// приставка комнаты в именах — r1-, r2-, r3-. Проверку на столкновение
// между сборщиками здесь сделать нельзя: каждый знает только себя.
// CollectorID — под каким именем сборщик известен центру. Команды
// помещения центр адресует именно ему.
func (c Config) CollectorID() string {
	if c.Room.ID != "" {
		return c.Room.ID
	}
	return c.Sensors.ID
}

func (c Config) OutputDeviceID(outputID string) string {
	return outputID
}

// IsLocalOutput сообщает, принадлежит ли идентификатор выходу сборщика.
// Надзор за живостью к таким устройствам не применяется: они местные,
// и «молчание» для них ничего не значит.
func (c Config) IsLocalOutput(deviceID string) bool {
	for _, o := range c.Outputs.Lines {
		if c.OutputDeviceID(o.ID) == deviceID {
			return true
		}
	}
	return false
}

// LocalOutputIDs — идентификаторы всех выходов для надзора за живостью.
func (c Config) LocalOutputIDs() []string {
	ids := make([]string, 0, len(c.Outputs.Lines))
	for _, o := range c.Outputs.Lines {
		ids = append(ids, c.OutputDeviceID(o.ID))
	}
	return ids
}

type Center struct {
	URL      string        `yaml:"url"`       // база API центра
	Token    string        `yaml:"token"`     // токен сборщика
	Every    time.Duration `yaml:"every"`     // как часто выгружать
	Timeout  time.Duration `yaml:"timeout"`   // таймаут запроса
	MaxBatch int           `yaml:"max_batch"` // сколько записей за раз
}

type Buffer struct {
	Path string `yaml:"path"` // файл журнала
	// Предел числа записей. По достижении вытесняются самые старые
	// показания; события реле не вытесняются никогда.
	MaxRecords int `yaml:"max_records"`
}

type DeviceMap struct {
	Addr string `yaml:"addr"` // IP платы
	ID   string `yaml:"id"`   // идентификатор устройства
	// Порт, на котором плата принимает команды (NET_LOCAL_PORT в прошивке).
	// Ноль — DefaultDevicePort.
	Port int `yaml:"port,omitempty"`
	// Настройки, которые досылаются плате после её перезапуска:
	// плата их не хранит (ARCHITECTURE.md §7.5).
	Limits *Limits `yaml:"limits,omitempty"`
}

// Limits — настройки платы, досылаемые после её перезапуска.
//
// Тепловых уставок здесь нет: защитой ведает сборщик, у которого реле
// пушки и свой датчик.
type Limits struct {
	WheelMM  uint32 `yaml:"wheel_mm"`  // длина окружности колеса
	PeriodMS uint32 `yaml:"period_ms"` // период отчётов телеметрии
}

type Sensors struct {
	Enabled bool          `yaml:"enabled"`
	Device  string        `yaml:"device"` // например /dev/i2c-1
	Addr    uint8         `yaml:"addr"`   // адрес BMP280 на шине
	Every   time.Duration `yaml:"every"`
	ID      string        `yaml:"id"` // под каким идентификатором отдавать
}

// DefaultDevicePort — порт платы по умолчанию: NET_LOCAL_PORT прошивки.
const DefaultDevicePort = 5006

// Load читает конфигурацию из файла.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("чтение конфигурации: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("разбор конфигурации: %w", err)
	}
	cfg.applyDefaults()

	return cfg, cfg.Validate()
}

// Default возвращает настройки, с которыми сборщик осмысленно работает
// без файла конфигурации.
func Default() Config {
	return Config{
		Site:    "default",
		Listen:  ":5005",
		HTTP:    ":8080",
		Silence: 3 * time.Second,
		Center: Center{
			Every:    5 * time.Second,
			Timeout:  10 * time.Second,
			MaxBatch: 500,
		},
		Buffer: Buffer{
			Path:       "buffer.jsonl",
			MaxRecords: 200_000,
		},
	}
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.Site == "" {
		c.Site = d.Site
	}
	if c.Center.Every <= 0 {
		c.Center.Every = d.Center.Every
	}
	if c.Center.Timeout <= 0 {
		c.Center.Timeout = d.Center.Timeout
	}
	if c.Center.MaxBatch <= 0 {
		c.Center.MaxBatch = d.Center.MaxBatch
	}
	if c.Buffer.Path == "" {
		c.Buffer.Path = d.Buffer.Path
	}
	if c.Buffer.MaxRecords <= 0 {
		c.Buffer.MaxRecords = d.Buffer.MaxRecords
	}
	if c.Sensors.Every <= 0 {
		c.Sensors.Every = time.Second
	}
	if c.Sensors.ID == "" {
		c.Sensors.ID = c.Site + "-rpi"
	}
	if c.Outputs.Chip == "" {
		c.Outputs.Chip = "/dev/gpiochip0"
	}
	for i := range c.Outputs.Lines {
		// Род выхода не угадывается: не указан — значит ключ без
		// тепловой логики. Молча навязанная защита заблокировала бы
		// включение стробоскопа из-за отсутствия датчика.
		if c.Outputs.Lines[i].Kind == "" {
			c.Outputs.Lines[i].Kind = OutputSwitch
		}
	}
}

func (c Config) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("не задан адрес приёма телеметрии")
	}
	// Предел батча задан контрактом: центр отвергнет пачку больше него.
	if c.Center.MaxBatch > 500 {
		return fmt.Errorf("max_batch превышает предел контракта (500)")
	}
	seen := make(map[string]string, len(c.Devices))
	for _, d := range c.Devices {
		if d.Addr == "" || d.ID == "" {
			return fmt.Errorf("в карте устройств нужны и addr, и id")
		}
		if prev, ok := seen[d.Addr]; ok {
			return fmt.Errorf("адрес %s назначен и %s, и %s", d.Addr, prev, d.ID)
		}
		seen[d.Addr] = d.ID
		if d.Limits != nil && d.Limits.WheelMM != 0 &&
			(d.Limits.WheelMM < 900 || d.Limits.WheelMM > 2500) {
			return fmt.Errorf("устройство %s: длина окружности колеса %d мм вне границ (900–2500)",
				d.ID, d.Limits.WheelMM)
		}
	}

	if c.Outputs.Enabled {
		if err := c.validateOutputs(); err != nil {
			return err
		}
	}
	return nil
}

// validateOutputs проверяет реле до того, как они будут запрошены.
//
// Две линии с одним номером или два выхода с одним именем — это
// перепутанная обвязка, и обнаружиться она должна при запуске, а не
// щелчком не того реле во время занятия.
func (c Config) validateOutputs() error {
	if len(c.Outputs.Lines) == 0 {
		return fmt.Errorf("выходы включены, но ни один не описан")
	}

	ids := make(map[string]struct{}, len(c.Outputs.Lines))
	lines := make(map[uint32]string, len(c.Outputs.Lines))

	for _, o := range c.Outputs.Lines {
		if o.ID == "" {
			return fmt.Errorf("у выхода на линии %d не задан id", o.Line)
		}
		if _, dup := ids[o.ID]; dup {
			return fmt.Errorf("выход %q описан дважды", o.ID)
		}
		ids[o.ID] = struct{}{}

		if prev, dup := lines[o.Line]; dup {
			return fmt.Errorf("линия %d назначена и выходу %q, и %q",
				o.Line, prev, o.ID)
		}
		lines[o.Line] = o.ID

		switch o.Kind {
		case OutputSwitch:
			// Уставки у ключа — почти наверняка недосмотр, и принять их
			// молча опаснее, чем отказать: оператор будет считать, что
			// у выхода есть тепловая защита.
			if o.LimitC != 0 || o.RestoreC != 0 || len(o.TempFrom) > 0 {
				return fmt.Errorf("выход %q описан как ключ, но ему заданы тепловые уставки: "+
					"укажите kind: heater или уберите их", o.ID)
			}

		case OutputHeater:
			if o.RestoreC >= o.LimitC {
				return fmt.Errorf("выход %q: температура возврата (%.1f) должна быть ниже порога (%.1f)",
					o.ID, o.RestoreC, o.LimitC)
			}
			if o.LimitC <= 0 || o.LimitC > 125 {
				return fmt.Errorf("выход %q: порог %.1f вне диапазона датчика (0–125 °C)",
					o.ID, o.LimitC)
			}
			for _, id := range o.TempFrom {
				if _, ok := seen2(c, id); !ok {
					return fmt.Errorf("выход %q: в temp_from указано неизвестное устройство %q",
						o.ID, id)
				}
			}

		default:
			return fmt.Errorf("выход %q: неизвестный род %q (ожидается heater или switch)",
				o.ID, o.Kind)
		}
	}
	return nil
}

// seen2 ищет устройство по идентификатору — в карте плат или среди
// собственных датчиков сборщика.
func seen2(c Config, id string) (string, bool) {
	if id == c.Sensors.ID {
		return id, true
	}
	for _, d := range c.Devices {
		if d.ID == id {
			return id, true
		}
	}
	return "", false
}

// DeviceID возвращает идентификатор устройства по адресу источника.
func (c Config) DeviceID(addr string) (string, bool) {
	for _, d := range c.Devices {
		if d.Addr == addr {
			return d.ID, true
		}
	}
	return "", false
}

// LimitsFor возвращает уставки устройства, если они заданы.
func (c Config) LimitsFor(id string) (Limits, bool) {
	for _, d := range c.Devices {
		if d.ID == id && d.Limits != nil {
			return *d.Limits, true
		}
	}
	return Limits{}, false
}

// AddrFor возвращает адрес и порт платы по идентификатору: команды надо
// отправлять на конкретный адрес, а не «в ответ на телеметрию» —
// последняя датаграмма могла прийти откуда угодно.
func (c Config) AddrFor(id string) (string, int, bool) {
	for _, d := range c.Devices {
		if d.ID == id {
			port := d.Port
			if port == 0 {
				port = DefaultDevicePort
			}
			return d.Addr, port, true
		}
	}
	return "", 0, false
}
