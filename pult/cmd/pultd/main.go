// Команда pultd — служба пульта управления тренажёрным комплексом.
//
// Пульт стоит отдельным устройством рядом с залами: три расширителя
// MCP23017 на шине I2C, на каждом кнопки и лампы одного помещения,
// плюс общий аварийный грибок.
//
// Архитектура обмена: пульт → центр → сборщики помещений. Обычная
// кнопка всегда идёт через центр — там сценарий, журнал и состояние.
// Мимо центра ходит только аварийная остановка, и только когда центр
// не отвечает: останавливать зал нельзя ставить в зависимость от
// одного узла.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ArnurKabdylkak/lss/pult/internal/adapter/outbound/center"
	"github.com/ArnurKabdylkak/lss/pult/internal/adapter/outbound/direct"
	"github.com/ArnurKabdylkak/lss/pult/internal/adapter/outbound/mcp"
	"github.com/ArnurKabdylkak/lss/pult/internal/app/pult"
	"github.com/ArnurKabdylkak/lss/pult/internal/config"
	"github.com/ArnurKabdylkak/lss/pult/internal/domain/console"
)

func main() {
	if err := run(); err != nil {
		slog.Error("пульт остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", "/etc/mchs/pult.yml", "файл настроек пульта")
	probe := flag.Bool("probe", false,
		"проверить шину и выйти: какие расширители отзываются")
	watch := flag.Bool("watch", false,
		"показывать нажатия кнопок, не трогая центр: для проверки монтажа")
	calib := flag.Bool("calibrate", false,
		"снять раскладку кнопок с железа: назову узел — нажмите его кнопку")
	calibOut := flag.String("calibrate-out", "",
		"куда записать снятую раскладку (пусто — показать на экране)")
	lampsCheck := flag.Bool("lamps-test", false,
		"зажечь лампы по очереди: проверка, что подсветка принадлежит своей кнопке")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, con, err := config.Load(*path)
	if err != nil {
		return err
	}

	bus, err := mcp.Open(cfg.I2C.Device)
	if err != nil {
		return err
	}
	defer bus.Close()

	if *probe {
		// Проверка тоже приводит микросхемы в известное состояние:
		// иначе читались бы остатки от прежнего запуска, а по ним
		// нельзя отличить неверный монтаж от неверной настройки.
		if err := setupChips(bus, con, log); err != nil {
			return err
		}
		return probeBus(bus, con, log)
	}
	if *calib {
		if err := setupChips(bus, con, log); err != nil {
			return err
		}
		return calibrate(bus, con, *calibOut)
	}
	if *lampsCheck {
		if err := setupChips(bus, con, log); err != nil {
			return err
		}
		return lampsTest(bus, con, 1500*time.Millisecond)
	}
	if *watch {
		// Наблюдение тоже настраивает микросхемы: иначе смотрели бы
		// на то, что осталось от прошлого запуска, и «висящий» вход
		// не отличить от невключённой подтяжки.
		if err := setupChips(bus, con, log); err != nil {
			return err
		}
		return watchButtons(bus, con)
	}

	if err := cfg.CheckCenter(); err != nil {
		return err
	}

	if err := setupChips(bus, con, log); err != nil {
		return err
	}

	client := center.New(cfg.Center.URL, cfg.Center.Login, cfg.Center.Password,
		cfg.Center.Timeout, log)

	urls := cfg.CollectorURLs()
	back := direct.New(urls, cfg.DirectToken, cfg.Center.Timeout, log)
	if len(urls) == 0 {
		log.Warn("прямой путь аварии не настроен: без центра остановка не уйдёт")
	}

	svc := pult.New(con, bus, adapter{client}, back, pult.Options{
		Scan:        cfg.Timing.Scan,
		Debounce:    cfg.Timing.Debounce,
		Poll:        cfg.Timing.Poll,
		Blink:       cfg.Timing.Blink,
		AlarmReason: cfg.Emergency.Reason,
	}, log)

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("пульт запущен", "центр", cfg.Center.URL, "помещений", len(con.Rooms))
	return svc.Run(ctx)
}

// setupChips настраивает микросхемы по раскладке: где кнопка — вход с
// подтяжкой, где лампа — выход. Маски считаются из той же раскладки,
// чтобы схема и код не разъехались.
func setupChips(bus *mcp.Bus, con console.Console, log *slog.Logger) error {
	for _, addr := range con.Addrs() {
		buttons, lamps := con.Masks(addr)
		if err := bus.Setup(addr, buttons, lamps, con.ActiveHigh, con.Sink); err != nil {
			return err
		}
		log.Info("расширитель настроен",
			"addr", fmt.Sprintf("0x%02X", addr),
			"кнопок", popcount(buttons[0])+popcount(buttons[1]),
			"ламп", popcount(lamps[0])+popcount(lamps[1]))
	}
	return nil
}

// probeBus показывает, какие расширители отзываются на шине.
//
// Заменяет i2cdetect: в закрытом контуре пакет i2c-tools взять
// неоткуда, а первый вопрос при монтаже — видно ли микросхемы вообще.
func probeBus(bus *mcp.Bus, con console.Console, log *slog.Logger) error {
	// Проверяется весь диапазон адресов MCP23017, а не только
	// занятые: перепутанные ноги A0/A1/A2 дают живую микросхему на
	// чужом адресе, и это нужно увидеть.
	var missing []string
	for addr := uint8(0x20); addr <= 0x27; addr++ {
		err := bus.Probe(addr)
		want := wanted(con, addr)

		switch {
		case err == nil && want != "":
			fmt.Printf("  0x%02X  отзывается  — %s\n", addr, want)
			dumpPorts(bus, con, addr)
		case err == nil:
			fmt.Printf("  0x%02X  отзывается  — в раскладке не числится\n", addr)
		case want != "":
			fmt.Printf("  0x%02X  НЕ отвечает — ожидается %s\n", addr, want)
			missing = append(missing, fmt.Sprintf("0x%02X (%s)", addr, want))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("не отвечают: %s", strings.Join(missing, ", "))
	}
	log.Info("все расширители на месте")
	return nil
}

// watchButtons показывает изменения на кнопках, пока не прервут.
//
// Нужно при монтаже: нажали — увидели, какая нога дёрнулась и в какую
// сторону. Центр при этом не трогается, команды никуда не уходят.
func watchButtons(bus *mcp.Bus, con console.Console) error {
	fmt.Println("Нажимайте кнопки — покажу, что меняется. Выход: Ctrl+C.")
	fmt.Printf("Полярность в настройках: %s\n\n", polarity(con.ActiveHigh))

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	prev := map[uint8][2]byte{}
	first := true
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()

	for {
		for _, addr := range con.Addrs() {
			raw, err := bus.ReadStable(addr)
			if err != nil {
				return err
			}
			was, seen := prev[addr]
			prev[addr] = raw
			if !seen {
				// Показываются только ноги раскладки: на свободных
				// инверсии нет, и их единицы — это покой подтяжки,
				// а не нажатие.
				btn, _ := con.Masks(addr)
				fmt.Printf("  0x%02X  исходно A=%08b B=%08b\n",
					addr, raw[0]&btn[0], raw[1]&btn[1])
				continue
			}
			if was == raw {
				continue
			}
			for _, pin := range allPins() {
				if (was[pin.Port]^raw[pin.Port])&pin.Mask() == 0 {
					continue
				}
				// Ноги, которых нет в раскладке, пропускаются:
				// смотреть при монтаже надо на свои кнопки, а не на
				// чужие ноги.
				if where(con, addr, pin) == "в раскладке не числится" {
					continue
				}
				now := "отпущена"
				if raw[pin.Port]&pin.Mask() != 0 {
					now = "НАЖАТА"
				}
				fmt.Printf("  0x%02X  %-3s %-8s  — %s\n",
					addr, pin, now, where(con, addr, pin))
			}
		}
		if first {
			fmt.Println()
			first = false
		}

		select {
		case <-ctx.Done():
			fmt.Println("\nостановлено")
			return nil
		case <-t.C:
		}
	}
}

func polarity(activeHigh bool) string {
	if activeHigh {
		return "кнопка подаёт питание (buttons_active_high: true)"
	}
	return "кнопка сажает на землю (умолчание)"
}

// where сообщает, что по раскладке висит на этой ноге.
func where(con console.Console, addr uint8, pin console.Pin) string {
	for _, r := range con.Rooms {
		if r.Addr != addr {
			continue
		}
		for _, u := range r.Units {
			if u.Button == pin {
				return fmt.Sprintf("%s, %s", r.ID, u.Name)
			}
		}
	}
	if con.Emergency.Addr == addr && con.Emergency.Button == pin {
		return "аварийная кнопка"
	}
	return "в раскладке не числится"
}

// dumpPorts показывает, как настроены порты и что на них сейчас.
//
// Единица в «нажато» при неподключённых кнопках означает, что вход
// висит в воздухе: подтяжка не включилась или кнопка посажена не на
// землю. Без этой строки такое ищут тестером полдня.
func dumpPorts(bus *mcp.Bus, con console.Console, addr uint8) {
	ports, err := bus.Dump(addr)
	if err != nil {
		fmt.Printf("        регистры не прочитаны: %v\n", err)
		return
	}
	buttons, lamps := con.Masks(addr)
	for i, p := range ports {
		name := "A"
		if i == 1 {
			name = "B"
		}
		fmt.Printf("        порт %s: кнопки=%08b лампы=%08b подтяжка=%08b нажато=%08b\n",
			name, buttons[i], lamps[i], p.Pull, p.Pins&buttons[i])
	}
}

// wanted сообщает, что должно стоять на этом адресе по раскладке.
func wanted(con console.Console, addr uint8) string {
	for _, r := range con.Rooms {
		if r.Addr == addr {
			name := r.Name
			if name == "" {
				name = r.ID
			}
			return fmt.Sprintf("%s, кнопок %d", name, len(r.Units))
		}
	}
	if con.Emergency.Addr == addr {
		return "аварийная кнопка"
	}
	return ""
}

// adapter переводит ответ центра в то, что нужно пульту.
//
// Пульт оперирует узлами помещений, а центр отдаёт комнаты целиком —
// со сменой, составом и камерами. Перекладывание держим на краю, а не
// в службе: иначе форма ответа API просочится в логику кнопок.
type adapter struct{ c *center.Client }

func (a adapter) State(ctx context.Context) (pult.Snapshot, error) {
	snap, err := a.c.State(ctx)
	if err != nil {
		return pult.Snapshot{}, err
	}

	out := pult.Snapshot{Units: map[pult.UnitKey]pult.UnitState{}, Alarm: snap.Alarm}
	for _, r := range snap.Rooms {
		for _, u := range r.Equipment {
			out.Units[pult.UnitKey{Room: r.ID, Unit: u.Unit}] = pult.UnitState{
				On: u.On, Wait: u.Wait,
			}
		}
	}
	return out, nil
}

func (a adapter) SetUnit(ctx context.Context, room, unit string, on bool) error {
	return a.c.SetUnit(ctx, room, unit, on)
}

func (a adapter) Alarm(ctx context.Context, reason string) error {
	return a.c.Alarm(ctx, reason)
}

func popcount(b byte) int {
	n := 0
	for ; b != 0; b >>= 1 {
		n += int(b & 1)
	}
	return n
}
