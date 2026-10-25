// Package config читает настройки пульта из YAML.
//
// Раскладка кнопок правится инженером на месте: какая нога к какой
// кнопке припаяна, выясняется при монтаже, а не при сборке.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/pult/internal/domain/console"
	"gopkg.in/yaml.v3"
)

// Config — настройки службы целиком.
type Config struct {
	Center    Center    `yaml:"center"`
	I2C       I2C       `yaml:"i2c"`
	Emergency Emergency `yaml:"emergency"`
	Rooms     []Room    `yaml:"rooms"`
	// ButtonsActiveHigh — кнопки подают питание, а не сажают на
	// землю. Умолчание (false) — обычный монтаж на землю.
	ButtonsActiveHigh bool `yaml:"buttons_active_high"`
	// LampsSourceVoltage — светодиод зажигается подачей питания, а не
	// прижатием к земле. Умолчание (false) — обычный монтаж: катод на
	// ногу, анод к питанию.
	LampsSourceVoltage bool        `yaml:"lamps_source_voltage"`
	Collectors         []Collector `yaml:"collectors"`
	// DirectToken — общий секрет для прямой аварии в сборщики.
	// Путь запасной и ходит в обход центра, поэтому сборщик должен
	// отличать пульт от случайного запроса в сети.
	DirectToken string `yaml:"direct_token"`
	Timing      Timing `yaml:"timing"`

	// path — откуда прочитаны настройки: нужен только для внятных
	// сообщений об ошибке.
	path string
}

// Center — куда ходит пульт за состоянием и куда шлёт команды.
type Center struct {
	URL   string `yaml:"url"`
	Login string `yaml:"login"`
	// Password берётся из окружения: файл правится на объекте и
	// лежит на карте, которую можно вынуть.
	Password string        `yaml:"password"`
	Timeout  time.Duration `yaml:"timeout"`
}

type I2C struct {
	Device string `yaml:"device"`
}

// Emergency — аварийный грибок.
type Emergency struct {
	Addr           hexAddr `yaml:"addr"`
	Button         string  `yaml:"button"`
	Lamp           string  `yaml:"lamp"`
	NormallyClosed *bool   `yaml:"normally_closed"`
	Reason         string  `yaml:"reason"`
}

// Room — помещение: одна микросхема, свои кнопки.
type Room struct {
	ID    string  `yaml:"id"`
	Name  string  `yaml:"name"`
	Addr  hexAddr `yaml:"addr"`
	Units []Unit  `yaml:"units"`
}

// Unit — кнопка с лампой.
type Unit struct {
	Unit   string `yaml:"unit"`
	Name   string `yaml:"name"`
	Button string `yaml:"button"`
	Lamp   string `yaml:"lamp"`
}

// Collector — сборщик помещения для прямой аварии, минуя центр.
type Collector struct {
	Room string `yaml:"room"`
	URL  string `yaml:"url"`
}

// Timing — периоды опроса.
type Timing struct {
	Scan     time.Duration `yaml:"scan"`
	Debounce time.Duration `yaml:"debounce"`
	Poll     time.Duration `yaml:"poll"`
	Blink    time.Duration `yaml:"blink"`
}

// hexAddr принимает адрес и числом, и строкой «0x20»: в документации
// на MCP23017 адреса шестнадцатеричные, и переводить их в уме при
// правке файла — лишний повод ошибиться.
type hexAddr uint8

func (a *hexAddr) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		var n uint8
		if err := node.Decode(&n); err != nil {
			return fmt.Errorf("адрес %q не разобран", node.Value)
		}
		*a = hexAddr(n)
		return nil
	}

	raw = strings.TrimSpace(strings.ToLower(raw))
	base := 10
	if strings.HasPrefix(raw, "0x") {
		raw, base = raw[2:], 16
	}
	v, err := strconv.ParseUint(raw, base, 8)
	if err != nil {
		return fmt.Errorf("адрес %q не разобран: %w", node.Value, err)
	}
	*a = hexAddr(v)
	return nil
}

// Load читает файл и собирает из него модель пульта.
func Load(path string) (Config, console.Console, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, console.Console{}, fmt.Errorf("чтение настроек: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, console.Console{}, fmt.Errorf("разбор %s: %w", path, err)
	}
	cfg.defaults()

	con, err := cfg.console()
	if err != nil {
		return Config{}, console.Console{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := con.Validate(); err != nil {
		return Config{}, console.Console{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.path = path
	return cfg, con, nil
}

// CheckCenter проверяет, с чем пульт пойдёт в центр.
//
// Отдельно от Load намеренно: проверка шины на месте делается до того,
// как секреты попали в окружение службы, и требовать их ради неё
// значило бы оставить монтажника без диагностики.
func (c Config) CheckCenter() error {
	if c.Center.URL == "" {
		return fmt.Errorf("%s: не задан адрес центра", c.path)
	}
	if c.Center.Password == "" {
		return fmt.Errorf(
			"%s: не задан пароль пульта (обычно подставляется из окружения)", c.path)
	}
	return nil
}

func (c *Config) defaults() {
	if c.I2C.Device == "" {
		c.I2C.Device = "/dev/i2c-1"
	}
	if c.Center.Timeout <= 0 {
		c.Center.Timeout = 3 * time.Second
	}
	// Пароль пульта держится в окружении, а в файле стоит ссылка на
	// переменную: файл уезжает в репозиторий вместе с раскладкой.
	c.Center.Password = os.ExpandEnv(c.Center.Password)
	c.Center.Login = os.ExpandEnv(c.Center.Login)
	c.DirectToken = os.ExpandEnv(c.DirectToken)
	if c.Center.Login == "" {
		c.Center.Login = "pult"
	}
	if c.Emergency.NormallyClosed == nil {
		// Умолчание — нормально замкнутый грибок: так обрыв линии
		// выглядит как нажатие, а не как исправная кнопка.
		yes := true
		c.Emergency.NormallyClosed = &yes
	}
}

// console превращает настройки в модель пульта.
func (c Config) console() (console.Console, error) {
	out := console.Console{
		Rooms:      make([]console.Room, 0, len(c.Rooms)),
		ActiveHigh: c.ButtonsActiveHigh,
		Sink:       !c.LampsSourceVoltage,
	}

	for _, r := range c.Rooms {
		room := console.Room{
			ID:    r.ID,
			Name:  r.Name,
			Addr:  uint8(r.Addr),
			Units: make([]console.Unit, 0, len(r.Units)),
		}
		for _, u := range r.Units {
			btn, err := console.ParsePin(u.Button)
			if err != nil {
				return console.Console{}, fmt.Errorf("помещение %s, узел %s: %w", r.ID, u.Unit, err)
			}
			// Лампа необязательна: там, где кнопки занимают оба
			// порта, ног под подсветку не остаётся.
			var lamp console.Pin
			if u.Lamp != "" {
				if lamp, err = console.ParsePin(u.Lamp); err != nil {
					return console.Console{}, fmt.Errorf("помещение %s, узел %s: %w",
						r.ID, u.Unit, err)
				}
			}
			room.Units = append(room.Units, console.Unit{
				Unit: u.Unit, Name: u.Name, Button: btn,
				Lamp: lamp, HasLamp: u.Lamp != "",
			})
		}
		out.Rooms = append(out.Rooms, room)
	}

	btn, err := console.ParsePin(c.Emergency.Button)
	if err != nil {
		return console.Console{}, fmt.Errorf("аварийная кнопка: %w", err)
	}
	var lamp console.Pin
	if c.Emergency.Lamp != "" {
		if lamp, err = console.ParsePin(c.Emergency.Lamp); err != nil {
			return console.Console{}, fmt.Errorf("аварийная лампа: %w", err)
		}
	}
	out.Emergency = console.Emergency{
		Addr:           uint8(c.Emergency.Addr),
		Button:         btn,
		Lamp:           lamp,
		HasLamp:        c.Emergency.Lamp != "",
		NormallyClosed: *c.Emergency.NormallyClosed,
	}
	return out, nil
}

// CollectorURLs — адреса сборщиков для прямой аварии.
func (c Config) CollectorURLs() []string {
	out := make([]string, 0, len(c.Collectors))
	for _, col := range c.Collectors {
		if col.URL != "" {
			out = append(out, strings.TrimRight(col.URL, "/"))
		}
	}
	return out
}
