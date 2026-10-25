package app

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// Управление выводами Raspberry Pi через символьное устройство
// /dev/gpiochipN (ABI v2 ядра Linux).
//
// Не sysfs: /sys/class/gpio объявлен устаревшим и в новых ядрах
// отключается, а здесь висит реле тепловой пушки — терять управление
// из-за обновления ядра нельзя. Внешних библиотек тоже нет: контур
// закрытый, и один ioctl дешевле зависимости, которую надо вносить.

const (
	gpioMaxLines   = 64
	gpioMaxName    = 32
	gpioMaxAttrs   = 10
	gpioMaxOffsets = gpioMaxLines

	// _IOWR(0xB4, 0x07, struct gpio_v2_line_request), размер 592 байта.
	ioctlGetLine = 0xC250B407
	// _IOWR(0xB4, 0x0F, struct gpio_v2_line_values), размер 16 байт.
	ioctlSetValues = 0xC010B40F

	flagOutput    = 1 << 3
	flagActiveLow = 1 << 1
	flagOpenDrain = 1 << 6

	// GPIO_V2_LINE_ATTR_ID_OUTPUT_VALUES
	attrOutputValues = 2
)

type lineAttribute struct {
	ID      uint32
	Padding uint32
	Value   uint64 // flags / values / debounce — по смыслу ID
}

type lineConfigAttribute struct {
	Attr lineAttribute
	Mask uint64
}

type lineConfig struct {
	Flags    uint64
	NumAttrs uint32
	Padding  [5]uint32
	Attrs    [gpioMaxAttrs]lineConfigAttribute
}

type lineRequest struct {
	Offsets         [gpioMaxOffsets]uint32
	Consumer        [gpioMaxName]byte
	Config          lineConfig
	NumLines        uint32
	EventBufferSize uint32
	Padding         [5]uint32
	FD              int32
}

type lineValues struct {
	Bits uint64
	Mask uint64
}

// GPIOLine — один запрошенный вывод на выход.
type GPIOLine struct {
	mu    sync.Mutex
	file  *os.File
	line  *os.File
	value bool
}

// GPIOOutput описывает запрашиваемый вывод.
//
// Это структура, а не список аргументов: три подряд идущих bool в
// вызове перепутать слишком легко, а цена ошибки здесь — реле пушки
// в неверном состоянии.
type GPIOOutput struct {
	Chip string // /dev/gpiochipN
	Line uint32 // номер вывода на чипе

	// ActiveLow описывает ключ, а не логику: модули реле с оптопарой
	// замыкаются низким уровнем. Инверсию делает ядро, поэтому дальше
	// по коду true всегда означает «включено», независимо от обвязки.
	ActiveLow bool

	// OpenDrain нужен модулям с логикой на 5 В: вывод либо стягивает
	// линию на землю, либо отпускает её в высокоимпедансное
	// состояние, но никогда не выдаёт своё напряжение.
	//
	// У модуля с оптопарой ток идёт от его питания в вывод RPi. Выдай
	// вывод 3,3 В «в противоход» 5 В — на светодиоде оптопары
	// останется разность, и реле может не отпустить до конца. В
	// открытом коллекторе выключенное состояние не выдаёт ничего,
	// и ток прекращается полностью.
	OpenDrain bool

	// Initial — состояние сразу после запроса линии, в той же логике,
	// что и Set: true означает «включено».
	Initial bool

	// Consumer — имя владельца линии, его показывает gpioinfo.
	Consumer string
}

// OpenGPIOOutput запрашивает вывод как выход.
func OpenGPIOOutput(out GPIOOutput) (*GPIOLine, error) {
	f, err := os.OpenFile(out.Chip, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("открытие %s: %w", out.Chip, err)
	}

	req := lineRequest{NumLines: 1}
	req.Offsets[0] = out.Line
	copy(req.Consumer[:gpioMaxName-1], out.Consumer)

	req.Config.Flags = flagOutput
	if out.ActiveLow {
		req.Config.Flags |= flagActiveLow
	}
	if out.OpenDrain {
		req.Config.Flags |= flagOpenDrain
	}

	// Начальное значение задаётся вместе с запросом линии: если
	// выставлять его следом, между запросом и записью вывод успеет
	// постоять в состоянии по умолчанию — для реле пушки это щелчок.
	req.Config.NumAttrs = 1
	req.Config.Attrs[0].Attr.ID = attrOutputValues
	req.Config.Attrs[0].Mask = 1
	if out.Initial {
		req.Config.Attrs[0].Attr.Value = 1
	}

	if err := ioctl(f.Fd(), ioctlGetLine, unsafe.Pointer(&req)); err != nil {
		f.Close()
		return nil, fmt.Errorf("запрос линии %s:%d: %w", out.Chip, out.Line, err)
	}

	return &GPIOLine{
		file:  f,
		line:  os.NewFile(uintptr(req.FD), fmt.Sprintf("%s:%d", out.Chip, out.Line)),
		value: out.Initial,
	}, nil
}

// Set выставляет вывод. true — включено (с учётом полярности ключа).
func (l *GPIOLine) Set(on bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	vals := lineValues{Mask: 1}
	if on {
		vals.Bits = 1
	}

	if err := ioctl(l.line.Fd(), ioctlSetValues, unsafe.Pointer(&vals)); err != nil {
		return fmt.Errorf("запись в линию: %w", err)
	}

	l.value = on
	return nil
}

// Value возвращает последнее выставленное значение.
func (l *GPIOLine) Value() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.value
}

// Close освобождает линию.
func (l *GPIOLine) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var first error
	if l.line != nil {
		first = l.line.Close()
	}
	if l.file != nil {
		if err := l.file.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func ioctl(fd uintptr, request uint32, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(request), uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
