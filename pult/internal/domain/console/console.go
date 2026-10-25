// Package console — модель пульта: какие кнопки и лампы какому узлу
// какого помещения соответствуют.
//
// Пульт не знает, что стоит за узлом: какое реле включает дымогенератор,
// решает сборщик помещения, а когда его включать — центр. Здесь только
// соответствие «нога расширителя ↔ узел», и ничего больше.
package console

import (
	"fmt"
	"strconv"
	"strings"
)

// Port — половина расширителя MCP23017. У микросхемы два восьмибитных
// порта, и нога адресуется парой «порт + номер». Значение совпадает с
// индексом порта в снимке состояния: 0 — A, 1 — B.
type Port byte

const (
	PortA Port = iota
	PortB
)

func (p Port) String() string {
	if p == PortB {
		return "B"
	}
	return "A"
}

// Pin — нога расширителя, например A0 или B5.
type Pin struct {
	Port Port
	Bit  uint8 // 0..7
}

func (p Pin) String() string { return fmt.Sprintf("%s%d", p.Port, p.Bit) }

// Mask — бит этой ноги в байте порта.
func (p Pin) Mask() byte { return 1 << p.Bit }

// ParsePin разбирает запись вида «A0», «b7».
func ParsePin(s string) (Pin, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if len(s) != 2 {
		return Pin{}, fmt.Errorf("нога %q: ожидается вид A0..B7", s)
	}

	var port Port
	switch s[0] {
	case 'A':
		port = PortA
	case 'B':
		port = PortB
	default:
		return Pin{}, fmt.Errorf("нога %q: порт бывает A или B", s)
	}

	bit, err := strconv.Atoi(s[1:])
	if err != nil || bit < 0 || bit > 7 {
		return Pin{}, fmt.Errorf("нога %q: номер бывает от 0 до 7", s)
	}
	return Pin{Port: port, Bit: uint8(bit)}, nil
}

// Unit — одна кнопка пульта вместе со своей лампой.
type Unit struct {
	// Unit — идентификатор узла в центре: smoke, heat, light…
	// Именно он уходит в команду, поэтому должен совпадать с
	// раскладкой центра буква в букву.
	Unit string
	Name string

	Button Pin
	// Lamp — лампа узла. Горит по подтверждению сборщика, а не по
	// нажатию: оператор у пульта должен видеть, что реле щёлкнуло,
	// а не то, что он нажал.
	Lamp Pin
	// HasLamp — лампа вообще есть. Подсветка не обязательна: на
	// пульте, где кнопки занимают оба порта, свободных ног под неё
	// не остаётся. Без этого признака нога «A0» у узла без лампы
	// была бы неотличима от настоящей лампы на A0 — и служба стала
	// бы выдавать на кнопку уровень, а нажатие замкнуло бы этот
	// выход на землю.
	HasLamp bool
}

// Room — помещение на пульте: одна микросхема на комнату.
type Room struct {
	// ID — идентификатор помещения в центре (room-1 и далее).
	ID   string
	Name string
	// Addr — адрес MCP23017 на шине: 0x20..0x27.
	Addr  uint8
	Units []Unit
}

// Console — пульт целиком.
type Console struct {
	Rooms []Room

	// Sink — светодиод зажигается прижатием ноги к земле: катод на
	// ногу, анод к питанию. Так собирают чаще всего. Иначе горящая
	// нога подаёт питание.
	Sink bool

	// ActiveHigh — как смонтированы кнопки.
	//
	// false (по умолчанию) — кнопка замыкает ногу на землю, а вверх
	// её держит подтяжка внутри микросхемы. Так делают обычно: лишних
	// деталей не нужно, а оборванный провод читается как «не нажато».
	//
	// true — кнопка подаёт на ногу питание, а вниз её тянет внешний
	// резистор. Встречается на готовых платах кнопок, и перепутать
	// это с первым вариантом нельзя: все кнопки окажутся нажатыми.
	ActiveHigh bool

	// Emergency — аварийный грибок. Он один на весь объект: авария
	// общая, и разделять её по комнатам нельзя (сценарий §7).
	Emergency Emergency
}

// Emergency — аварийная кнопка и её лампа.
type Emergency struct {
	Addr   uint8
	Button Pin
	// Lamp — лампа аварии. Горит, когда объект в аварии, независимо
	// от того, откуда её объявили: с пульта, из панели или по
	// датчику.
	Lamp    Pin
	HasLamp bool
	// NormallyClosed — грибок нормально замкнутый: в покое цепь
	// целая, нажатие её размыкает. Обрыв провода выглядит как
	// нажатие — отказ в безопасную сторону, так и задумано.
	NormallyClosed bool
}

// Masks — какие ноги микросхемы заняты кнопками и лампами.
// Индекс 0 — порт A, 1 — порт B.
func (c Console) Masks(addr uint8) (buttons, lamps [2]byte) {
	for _, r := range c.Rooms {
		if r.Addr != addr {
			continue
		}
		for _, u := range r.Units {
			buttons[u.Button.Port] |= u.Button.Mask()
			if u.HasLamp {
				lamps[u.Lamp.Port] |= u.Lamp.Mask()
			}
		}
	}
	if c.Emergency.Addr == addr {
		buttons[c.Emergency.Button.Port] |= c.Emergency.Button.Mask()
		if c.Emergency.HasLamp {
			lamps[c.Emergency.Lamp.Port] |= c.Emergency.Lamp.Mask()
		}
	}
	return buttons, lamps
}

// Validate проверяет пульт целиком до работы с железом.
//
// Ошибка в раскладке должна быть видна при старте: кнопка, молча
// привязанная к чужой ноге, обнаружится только в зале и только тогда,
// когда её нажмут.
func (c Console) Validate() error {
	if len(c.Rooms) == 0 {
		return fmt.Errorf("на пульте не описано ни одного помещения")
	}

	seenRoom := make(map[string]struct{}, len(c.Rooms))
	for _, r := range c.Rooms {
		if r.ID == "" {
			return fmt.Errorf("у помещения не задан идентификатор")
		}
		if _, dup := seenRoom[r.ID]; dup {
			return fmt.Errorf("помещение %s описано дважды", r.ID)
		}
		seenRoom[r.ID] = struct{}{}

		if r.Addr < 0x20 || r.Addr > 0x27 {
			return fmt.Errorf("помещение %s: адрес 0x%02X вне диапазона MCP23017 (0x20..0x27)",
				r.ID, r.Addr)
		}
		if len(r.Units) == 0 {
			return fmt.Errorf("помещение %s: ни одной кнопки", r.ID)
		}

		// Занятость ног проверяется в пределах микросхемы: две кнопки
		// на одной ноге — это замыкание, которое в зале выглядит как
		// «не работает одна из них».
		busy := make(map[Pin]string, len(r.Units)*2)
		for _, u := range r.Units {
			if u.Unit == "" {
				return fmt.Errorf("помещение %s: у кнопки не задан узел", r.ID)
			}
			pins := map[Pin]string{u.Button: "кнопка " + u.Unit}
			if u.HasLamp {
				pins[u.Lamp] = "лампа " + u.Unit
			}
			for pin, what := range pins {
				if prev, dup := busy[pin]; dup {
					return fmt.Errorf("помещение %s: нога %s занята дважды — %s и %s",
						r.ID, pin, prev, what)
				}
				busy[pin] = what
			}
		}

		if r.Addr == c.Emergency.Addr {
			if prev, dup := busy[c.Emergency.Button]; dup {
				return fmt.Errorf("нога %s аварийной кнопки занята: %s",
					c.Emergency.Button, prev)
			}
			if prev, dup := busy[c.Emergency.Lamp]; dup && c.Emergency.HasLamp {
				return fmt.Errorf("нога %s аварийной лампы занята: %s",
					c.Emergency.Lamp, prev)
			}
		}
	}

	if c.Emergency.Addr < 0x20 || c.Emergency.Addr > 0x27 {
		return fmt.Errorf("аварийная кнопка: адрес 0x%02X вне диапазона MCP23017",
			c.Emergency.Addr)
	}
	return nil
}

// Addrs перечисляет адреса микросхем пульта без повторов.
func (c Console) Addrs() []uint8 {
	seen := make(map[uint8]struct{}, len(c.Rooms)+1)
	out := make([]uint8, 0, len(c.Rooms)+1)
	for _, r := range c.Rooms {
		if _, dup := seen[r.Addr]; dup {
			continue
		}
		seen[r.Addr] = struct{}{}
		out = append(out, r.Addr)
	}
	if _, dup := seen[c.Emergency.Addr]; !dup {
		out = append(out, c.Emergency.Addr)
	}
	return out
}
