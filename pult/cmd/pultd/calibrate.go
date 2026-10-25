package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ArnurKabdylkak/lss/pult/internal/adapter/outbound/mcp"
	"github.com/ArnurKabdylkak/lss/pult/internal/domain/console"
)

// Калибровка пульта: раскладка снимается с железа, а не выдумывается.
//
// После монтажа никто не помнит наверняка, какая кнопка пришла на
// какую ногу — и ошибка здесь дорогая: оператор жмёт «вытяжка», а в
// зале включается тепловая пушка. Поэтому раскладку подтверждает сам
// монтажник, нажимая кнопки в том порядке, в котором их называют.

// calibrate проводит монтажника по всем узлам и записывает, какая
// нога отозвалась.
func calibrate(bus *mcp.Bus, con console.Console, out string) error {
	fmt.Println("Калибровка кнопок.")
	fmt.Println("Нажимайте кнопку, которую назову. Пропустить — Enter. Выход — Ctrl+C.")
	fmt.Println()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Enter читается отдельной горутиной: ждать строку и опрашивать
	// кнопки одновременно иначе не выйдет.
	skip := make(chan struct{}, 1)
	go func() {
		in := bufio.NewScanner(os.Stdin)
		for in.Scan() {
			select {
			case skip <- struct{}{}:
			default:
			}
		}
	}()

	found := map[string]console.Pin{} // "комната/узел" -> нога
	busy := map[string]string{}       // "адрес/нога" -> что на ней

	for _, r := range con.Rooms {
		fmt.Printf("  %s — %s\n", r.ID, roomName(r))
		for _, u := range r.Units {
			key := r.ID + "/" + u.Unit
			pin, addr, err := askPress(ctx, bus, con, skip, u.Name)
			if err != nil {
				return err
			}
			if pin == nil {
				fmt.Printf("      %-24s пропущено\n", u.Name)
				continue
			}

			where := fmt.Sprintf("0x%02X/%s", addr, pin)
			if prev, dup := busy[where]; dup {
				fmt.Printf("      %-24s та же нога, что у «%s» — пропущено\n",
					u.Name, prev)
				continue
			}
			if addr != r.Addr {
				fmt.Printf("      %-24s %s на 0x%02X — это чужая микросхема\n",
					u.Name, pin, addr)
				continue
			}

			busy[where] = u.Name
			found[key] = *pin
			fmt.Printf("      %-24s %s\n", u.Name, pin)
		}
		fmt.Println()
	}

	// Аварийный грибок снимается последним и отдельно: он нормально
	// замкнутый, и «нажатием» для него считается размыкание.
	fmt.Println("  Аварийный грибок")
	fmt.Println("      нажмите грибок (и отпустите поворотом)")
	pin, addr, err := askPress(ctx, bus, con, skip, "аварийная кнопка")
	if err != nil {
		return err
	}
	if pin != nil {
		fmt.Printf("      0x%02X %s\n", addr, pin)
		found["emergency"] = *pin
	}
	fmt.Println()

	return writeLayout(con, found, out)
}

// askPress ждёт нажатия любой кнопки и возвращает её ногу.
//
// Нажатие принимается только после того, как линия устоялась, и
// только по отпусканию: иначе дребезг даёт пол-нажатия, а удержание
// кнопки проскакивает сразу в следующий узел.
func askPress(ctx context.Context, bus *mcp.Bus, con console.Console,
	skip <-chan struct{}, what string) (*console.Pin, uint8, error) {

	fmt.Printf("      %-24s жду нажатия… ", what)

	const settle = 30 * time.Millisecond
	type seen struct {
		since time.Time
		level bool
	}
	state := map[string]*seen{}

	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil, 0, fmt.Errorf("калибровка прервана")
		case <-skip:
			return nil, 0, nil
		case <-t.C:
		}

		for _, addr := range con.Addrs() {
			raw, err := bus.ReadButtons(addr)
			if err != nil {
				return nil, 0, err
			}
			// Кнопки ищутся на обоих портах: раскладка не делит их
			// по назначению, и на объекте порт B бывает занят
			// кнопками целиком.
			for _, pin := range allPins() {
				key := fmt.Sprintf("%02X%s", addr, pin)
				level := raw[pin.Port]&pin.Mask() != 0

				st, ok := state[key]
				if !ok {
					state[key] = &seen{since: time.Now(), level: level}
					continue
				}
				if st.level != level {
					// Отпускание устоявшегося нажатия — это ответ.
					if !level && time.Since(st.since) >= settle {
						fmt.Println("есть")
						return &pin, addr, nil
					}
					st.level = level
					st.since = time.Now()
				}
			}
		}
	}
}

// allPins перечисляет все шестнадцать ног микросхемы.
func allPins() []console.Pin {
	out := make([]console.Pin, 0, 16)
	for _, port := range []console.Port{console.PortA, console.PortB} {
		for bit := range uint8(8) {
			out = append(out, console.Pin{Port: port, Bit: bit})
		}
	}
	return out
}

// writeLayout печатает снятую раскладку готовым куском настроек.
func writeLayout(con console.Console, found map[string]console.Pin, out string) error {
	var b strings.Builder
	b.WriteString("# Снято калибровкой: кнопки подтверждены нажатием.\n")
	b.WriteString("rooms:\n")

	for _, r := range con.Rooms {
		fmt.Fprintf(&b, "  - id: %s\n", r.ID)
		fmt.Fprintf(&b, "    name: %q\n", r.Name)
		fmt.Fprintf(&b, "    addr: \"0x%02X\"\n", r.Addr)
		b.WriteString("    units:\n")
		for _, u := range r.Units {
			btn := u.Button
			if pin, ok := found[r.ID+"/"+u.Unit]; ok {
				btn = pin
			}
			lamp := ""
			if u.HasLamp {
				lamp = u.Lamp.String()
			}
			fmt.Fprintf(&b, "      - {unit: %s, name: %q, button: %s, lamp: %q}\n",
				u.Unit, u.Name, btn, lamp)
		}
	}

	e := con.Emergency
	if pin, ok := found["emergency"]; ok {
		e.Button = pin
	}
	b.WriteString("\nemergency:\n")
	fmt.Fprintf(&b, "  addr: \"0x%02X\"\n", e.Addr)
	fmt.Fprintf(&b, "  button: %s\n", e.Button)
	lamp := ""
	if e.HasLamp {
		lamp = e.Lamp.String()
	}
	fmt.Fprintf(&b, "  lamp: %q\n", lamp)
	fmt.Fprintf(&b, "  normally_closed: %v\n", e.NormallyClosed)

	if out == "" {
		fmt.Print("\n", b.String())
		fmt.Println("\nПеренесите этот кусок в deploy/group_vars/pult.yml,")
		fmt.Println("иначе выкладка вернёт прежнюю раскладку.")
		return nil
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("\nРаскладка записана в %s\n", out)
	return nil
}

// lampsTest зажигает лампы по очереди: так проверяют, что подсветка
// кнопки принадлежит той же кнопке.
func lampsTest(bus *mcp.Bus, con console.Console, hold time.Duration) error {
	fmt.Println("Проверка ламп: зажигаю по очереди, смотрите, какая горит.")
	fmt.Println()

	for _, r := range con.Rooms {
		fmt.Printf("  %s\n", r.ID)
		for _, u := range r.Units {
			if !u.HasLamp {
				fmt.Printf("      %-24s лампы нет\n", u.Name)
				continue
			}
			var litA, litB byte
			if u.Lamp.Port == console.PortA {
				litA = u.Lamp.Mask()
			} else {
				litB = u.Lamp.Mask()
			}

			masks := func() (byte, byte) {
				_, lamps := con.Masks(r.Addr)
				return lamps[console.PortA], lamps[console.PortB]
			}
			maskA, maskB := masks()
			fmt.Printf("      %-24s %s\n", u.Name, u.Lamp)
			if err := bus.SetLamps(r.Addr, console.PortB, maskB, litB, con.Sink); err != nil {
				return err
			}
			if maskA != 0 {
				if err := bus.SetLamps(r.Addr, console.PortA, maskA, litA, con.Sink); err != nil {
					return err
				}
			}
			time.Sleep(hold)
		}

		_, lamps := con.Masks(r.Addr)
		maskA, maskB := lamps[console.PortA], lamps[console.PortB]
		if err := bus.SetLamps(r.Addr, console.PortB, maskB, 0, con.Sink); err != nil {
			return err
		}
		if maskA != 0 {
			if err := bus.SetLamps(r.Addr, console.PortA, maskA, 0, con.Sink); err != nil {
				return err
			}
		}
		fmt.Println()
	}
	return nil
}

func roomName(r console.Room) string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}
