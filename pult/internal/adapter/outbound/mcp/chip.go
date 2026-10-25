// Package mcp — драйвер расширителя портов MCP23017 на шине I2C.
//
// Работа идёт через ioctl напрямую, без сторонних библиотек: контур
// закрытый, тянуть в него зависимость ради двух регистров незачем.
package mcp

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ArnurKabdylkak/lss/pult/internal/domain/console"
)

// Регистры MCP23017 в банке 0 (IOCON.BANK = 0, значение после сброса).
const (
	regIODIRA = 0x00 // направление: 1 — вход, 0 — выход
	regIODIRB = 0x01
	regIPOLA  = 0x02 // инверсия входа
	regIPOLB  = 0x03
	regGPPUA  = 0x0C // подтяжка к питанию, 100 кОм
	regGPPUB  = 0x0D
	regGPIOA  = 0x12 // чтение ног
	regGPIOB  = 0x13
	regOLATA  = 0x14 // защёлка выходов
	regOLATB  = 0x15

	// IOCON лежит в разных местах в зависимости от режима адресации:
	// 0x0A в банке 0 и 0x05 в банке 1. Это и есть ловушка — пока
	// режим неизвестен, по одному адресу не достучаться.
	regIOCONBank0 = 0x0A
	regIOCONBank1 = 0x05

	i2cSlave  = 0x0703 // ioctl: выбрать адрес ведомого
	i2cRdwrOp = 0x0707 // ioctl: сделка из нескольких сообщений
	i2cRead   = 0x0001 // флаг сообщения: чтение
)

// Bus — шина I2C с несколькими расширителями.
//
// Файл устройства один на всю шину, а ведомый выбирается перед каждым
// обменом, поэтому обмен сериализуется мьютексом: без него два
// помещения, опрашиваемые одновременно, прочитали бы друг друга.
type Bus struct {
	mu   sync.Mutex
	file *os.File
	path string

	// cur — выбранный сейчас ведомый. Лишний ioctl на каждый байт
	// заметен при опросе кнопок раз в 20 мс.
	cur     uint8
	curSet  bool
	lastOut map[outKey]byte
}

// i2cMsg и i2cRdwr повторяют struct i2c_msg и
// struct i2c_rdwr_ioctl_data из linux/i2c-dev.h.
type i2cMsg struct {
	addr  uint16
	flags uint16
	len   uint16
	buf   uintptr
}

type i2cRdwr struct {
	msgs  uintptr
	nmsgs uint32
}

type outKey struct {
	addr uint8
	reg  uint8
}

// Open открывает шину. Микросхемы настраиваются отдельно — Setup.
func Open(path string) (*Bus, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("открытие шины %s: %w", path, err)
	}
	return &Bus{file: f, path: path, lastOut: map[outKey]byte{}}, nil
}

func (b *Bus) Close() error { return b.file.Close() }

// Setup готовит микросхему: кнопки — входы с подтяжкой, лампы —
// выходы в состоянии «погашено».
//
// Маски задаются на оба порта: раскладка не обязана делить порты по
// назначению, и на объекте кнопки могут занимать весь порт B целиком.
// Ноги, не занятые ни кнопкой, ни лампой, остаются входами — трогать
// их нельзя, на них может висеть чужая цепь.
//
// buttons и lamps — по ноге на разряд, индекс 0 — порт A, 1 — порт B.
func (b *Bus) Setup(addr uint8, buttons, lamps [2]byte, activeHigh, sink bool) error {
	// Режим адресации приводится к нулевому банку до всего
	// остального.
	//
	// У MCP23017 два набора адресов регистров, и какой действует,
	// задаёт бит BANK в IOCON. После сброса это банк 0, но
	// микросхема могла остаться в банке 1 от прежней прошивки или
	// чужого кода — тогда запись уходит не в тот регистр: подтяжка
	// не включается, а входы висят в воздухе и ловят наводки.
	//
	// Нулевой байт пишется по обоим адресам IOCON. В банке 1 первая
	// запись переводит микросхему в банк 0; в банке 0 по этому адресу
	// лежит GPINTENB, и ноль в нём безвреден — прерывания по входам
	// пульт не использует.
	for _, reg := range []byte{regIOCONBank1, regIOCONBank0} {
		if err := b.write(addr, reg, 0x00); err != nil {
			return fmt.Errorf("приведение 0x%02X к банку 0: %w", addr, err)
		}
	}

	type reg struct {
		addr uint8
		val  byte
	}
	var regs []reg

	for i, p := range [2]struct {
		iodir, gppu, ipol, olat byte
	}{
		{regIODIRA, regGPPUA, regIPOLA, regOLATA},
		{regIODIRB, regGPPUB, regIPOLB, regOLATB},
	} {
		btn, lamp := buttons[i], lamps[i]

		// Подтяжка ставится на все входы, а не только на кнопки.
		// Свободная нога без подтяжки висит в воздухе и звенит,
		// ловя наводку от соседей, — в журнале это неотличимо от
		// кнопки, которую кто-то дёргает.
		//
		// Инверсия — только на кнопках: это она делает «1»
		// значением «нажато», и свободным ногам она ни к чему.
		//
		// При монтаже на питание подтяжка не нужна вовсе: она
		// боролась бы с внешним резистором.
		pullup, invert := ^lamp, btn
		if activeHigh {
			pullup, invert = 0, 0
		}

		regs = append(regs,
			// Выходы — только под лампами. Всё остальное входы:
			// нога, занятая кнопкой, выходом быть не должна, а
			// свободную трогать незачем.
			reg{p.iodir, ^lamp},
			reg{p.gppu, pullup},
			reg{p.ipol, invert},
			reg{p.olat, lampsOff(lamp, sink)},
		)
	}

	// Запись проверяется чтением, и при расхождении повторяется.
	// Сорванная сделка на шине иначе оставила бы микросхему без
	// подтяжки — а это висящие входы, которые выглядят как нажатые
	// кнопки и ищутся потом тестером.
	for _, r := range regs {
		var last byte
		var err error
		for attempt := range 3 {
			if err = b.write(addr, r.addr, r.val); err != nil {
				continue
			}
			if last, err = b.read(addr, r.addr); err != nil {
				continue
			}
			if last == r.val {
				break
			}
			err = fmt.Errorf("записано %08b, прочитано %08b (попытка %d)",
				r.val, last, attempt+1)
		}
		if err != nil {
			return fmt.Errorf("настройка 0x%02X, регистр 0x%02X: %w", addr, r.addr, err)
		}
	}
	return nil
}

// read читает регистр под общим мьютексом.
func (b *Bus) read(addr, reg byte) (byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.selectSlave(addr); err != nil {
		return 0, err
	}
	return b.readReg(reg)
}

// lampsOff — уровень погашенных ламп.
//
// При катоде на ноге лампа горит от нуля, поэтому погашена она
// единицей. Это не мелочь настройки: на линии ламп через светодиоды
// опираются кнопки, и ноль на них читается как нажатие.
func lampsOff(lamps byte, sink bool) byte {
	if sink {
		return lamps
	}
	return 0x00
}

// Probe проверяет, отзывается ли микросхема по адресу.
//
// Нужно для диагностики на месте: i2cdetect в закрытом контуре взять
// неоткуда, а понять, видна ли микросхема, надо до того, как начнут
// искать обрыв в кнопках.
func (b *Bus) Probe(addr uint8) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.selectSlave(addr); err != nil {
		return err
	}
	// Читается регистр направления: он есть у любой живой MCP23017,
	// а чтение ничего не меняет.
	_, err := b.readReg(regIODIRA)
	return err
}

// PortState — настройка и текущее состояние одного порта.
type PortState struct {
	Dir, Pull, Invert, Pins byte
}

// Dump читает настроечные регистры обоих портов. По нему видно, так
// ли настроена микросхема, как задумано, и что она сейчас на входах
// видит.
func (b *Bus) Dump(addr uint8) ([2]PortState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out [2]PortState
	if err := b.selectSlave(addr); err != nil {
		return out, err
	}
	for i, regs := range [2][4]byte{
		{regIODIRA, regGPPUA, regIPOLA, regGPIOA},
		{regIODIRB, regGPPUB, regIPOLB, regGPIOB},
	} {
		vals := [4]byte{}
		for j, reg := range regs {
			v, err := b.readReg(reg)
			if err != nil {
				return out, err
			}
			vals[j] = v
		}
		out[i] = PortState{Dir: vals[0], Pull: vals[1], Invert: vals[2], Pins: vals[3]}
	}
	return out, nil
}

// Read читает оба порта микросхемы разом.
func (b *Bus) Read(addr uint8) (portA, portB byte, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.selectSlave(addr); err != nil {
		return 0, 0, err
	}
	a, err := b.readReg(regGPIOA)
	if err != nil {
		return 0, 0, err
	}
	p, err := b.readReg(regGPIOB)
	if err != nil {
		return 0, 0, err
	}
	return a, p, nil
}

// ReadButtons читает оба порта: кнопки могут стоять на любом.
//
// Чтение идёт двумя регистрами подряд на уже выбранном ведомом —
// это дешевле, чем выбирать его дважды.
func (b *Bus) ReadButtons(addr uint8) ([2]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out [2]byte
	if err := b.selectSlave(addr); err != nil {
		return out, err
	}
	for i, reg := range [2]byte{regGPIOA, regGPIOB} {
		v, err := b.readReg(reg)
		if err != nil {
			return out, err
		}
		out[i] = v
	}
	return out, nil
}

// ReadStable читает порт кнопок трижды и возвращает поразрядное
// большинство.
//
// Одиночная помеха на длинной линии портит один разряд одного
// чтения; чтобы она прошла дальше, ей нужно повториться в двух
// выборках из трёх подряд. Это не замена антидребезгу в службе, а
// первый рубеж: он снимает именно короткие выбросы, которые иначе
// попадают в счётчик как настоящий уровень.
func (b *Bus) ReadStable(addr uint8) ([2]byte, error) {
	var a, bb, c [2]byte
	for i, dst := range []*[2]byte{&a, &bb, &c} {
		v, err := b.ReadButtons(addr)
		if err != nil {
			return [2]byte{}, err
		}
		*dst = v
		if i < 2 {
			// Пауза между выборками: подряд идущие чтения поймали бы
			// одну и ту же помеху, и голосование ничего не дало бы.
			time.Sleep(200 * time.Microsecond)
		}
	}
	// Разряд принимается, если он совпал хотя бы в двух выборках.
	var out [2]byte
	for i := range out {
		out[i] = (a[i] & bb[i]) | (bb[i] & c[i]) | (a[i] & c[i])
	}
	return out, nil
}

// SetLamps зажигает лампы порта: lamps — какие ноги заняты лампами,
// lit — какие из них должны гореть.
//
// Направление ног не трогается: они заданы выходами при настройке и
// остаются ими. Гашение — это уровень, а не высокий импеданс: на
// линии ламп возвращаются кнопки, и отведённый выход оставил бы их
// без земли.
//
// sink — светодиод горит, когда нога прижата к земле (катод на ногу,
// анод к питанию). Иначе горящая нога подаёт питание.
func (b *Bus) SetLamps(addr uint8, port console.Port, lamps, lit byte, sink bool) error {
	reg := byte(regOLATB)
	if port == console.PortA {
		reg = regOLATA
	}

	// При катоде на ноге горящая лампа — это ноль, поэтому уровень
	// считается от обратного.
	level := lit
	if sink {
		level = lamps &^ lit
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Запись пропускается, если байт не изменился: шина общая, и
	// дёргать её ради тех же значений двадцать раз в секунду незачем.
	key := outKey{addr: addr, reg: reg}
	if prev, ok := b.lastOut[key]; ok && prev == level {
		return nil
	}
	if err := b.selectSlave(addr); err != nil {
		return err
	}
	if err := b.writeReg(reg, level); err != nil {
		return err
	}
	b.lastOut[key] = level
	return nil
}

// write выбирает ведомого и пишет регистр под общим мьютексом.
func (b *Bus) write(addr, reg, value byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.selectSlave(addr); err != nil {
		return err
	}
	return b.writeReg(reg, value)
}

// selectSlave вызывается под мьютексом.
func (b *Bus) selectSlave(addr uint8) error {
	if b.curSet && b.cur == addr {
		return nil
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, b.file.Fd(),
		uintptr(i2cSlave), uintptr(addr))
	if errno != 0 {
		b.curSet = false
		return fmt.Errorf("выбор адреса 0x%02X на шине %s: %w", addr, b.path, errno)
	}
	b.cur, b.curSet = addr, true
	return nil
}

// readReg читает регистр одной сделкой с повторным стартом.
//
// Раздельные write и read — это две сделки, между которыми шина
// отпускается. На Raspberry Pi при длинных проводах пульта этого
// хватало, чтобы время от времени прочитать не тот регистр: в
// журнале это выглядело как слетевшая настройка микросхемы.
// I2C_RDWR отдаёт драйверу обе части разом, и отпустить шину
// посередине он не может.
func (b *Bus) readReg(reg byte) (byte, error) {
	out := [1]byte{reg}
	in := [1]byte{}

	msgs := [2]i2cMsg{
		{addr: uint16(b.cur), len: 1, buf: uintptr(unsafe.Pointer(&out[0]))},
		{addr: uint16(b.cur), flags: i2cRead, len: 1, buf: uintptr(unsafe.Pointer(&in[0]))},
	}
	data := i2cRdwr{msgs: uintptr(unsafe.Pointer(&msgs[0])), nmsgs: 2}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, b.file.Fd(),
		uintptr(i2cRdwrOp), uintptr(unsafe.Pointer(&data)))
	runtime.KeepAlive(out)
	runtime.KeepAlive(in)
	runtime.KeepAlive(msgs)
	if errno != 0 {
		return 0, fmt.Errorf("чтение регистра 0x%02X: %w", reg, errno)
	}
	return in[0], nil
}

func (b *Bus) writeReg(reg, value byte) error {
	if _, err := b.file.Write([]byte{reg, value}); err != nil {
		return fmt.Errorf("запись регистра 0x%02X: %w", reg, err)
	}
	return nil
}
