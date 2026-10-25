package app

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Собственные датчики сборщика: BMP280 на шине I2C узла.
//
// Сборщик не только транслятор — у него есть своя шина, и его показания
// идут в тот же буфер и тот же контракт выгрузки, под своим
// идентификатором устройства.

const (
	bmp280ChipID   = 0x58
	regChipID      = 0xD0
	regCtrlMeas    = 0xF4
	regCalibration = 0x88
	regData        = 0xF7

	i2cSlave = 0x0703 // ioctl: выбрать адрес ведомого
)

// Sensor читает BMP280 по I2C.
type Sensor struct {
	cfg   Sensors
	site  string
	state *State
	buf   *DiskBuffer
	log   *slog.Logger

	// Свой датчик — основной источник для тепловой защиты: он читается
	// локально, без сети. Может быть nil, если выходов нет.
	outputs *Outputs

	mu    sync.Mutex
	file  *os.File
	calib calibration
}

type calibration struct {
	T1             uint16
	T2, T3         int16
	P1             uint16
	P2, P3, P4, P5 int16
	P6, P7, P8, P9 int16
}

func NewSensor(cfg Config, state *State, buf *DiskBuffer, log *slog.Logger) *Sensor {
	return &Sensor{cfg: cfg.Sensors, site: cfg.Site, state: state, buf: buf, log: log}
}

// Run опрашивает датчик до отмены контекста.
func (s *Sensor) Run(ctx context.Context) {
	if !s.cfg.Enabled {
		return
	}

	if err := s.open(); err != nil {
		// Отсутствие своего датчика не повод не работать: основная
		// задача сборщика — платы.
		s.log.Error("свой датчик недоступен, продолжаю без него", "err", err)
		return
	}
	defer s.close()

	ticker := time.NewTicker(s.cfg.Every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll()
		}
	}
}

func (s *Sensor) open() error {
	f, err := os.OpenFile(s.cfg.Device, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("открытие шины %s: %w", s.cfg.Device, err)
	}
	s.file = f

	if err := s.selectSlave(); err != nil {
		f.Close()
		return err
	}

	id, err := s.readReg(regChipID)
	if err != nil {
		f.Close()
		return fmt.Errorf("чтение chip id: %w", err)
	}
	if id != bmp280ChipID {
		f.Close()
		return fmt.Errorf("на адресе 0x%02X не BMP280 (chip id 0x%02X)", s.cfg.Addr, id)
	}

	if err := s.readCalibration(); err != nil {
		f.Close()
		return err
	}
	// Нормальный режим, оверсэмплинг x1 по обоим каналам.
	if err := s.writeReg(regCtrlMeas, 0x27); err != nil {
		f.Close()
		return err
	}

	s.log.Info("свой датчик подключён", "bus", s.cfg.Device, "addr", fmt.Sprintf("0x%02X", s.cfg.Addr))
	return nil
}

func (s *Sensor) selectSlave() error {
	// ioctl через syscall из стандартной библиотеки: тянуть x/sys ради
	// одного вызова не стоит, а сборщик остаётся почти без зависимостей.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, s.file.Fd(),
		uintptr(i2cSlave), uintptr(s.cfg.Addr))
	if errno != 0 {
		return fmt.Errorf("выбор адреса 0x%02X: %w", s.cfg.Addr, errno)
	}
	return nil
}

func (s *Sensor) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		s.file.Close()
		s.file = nil
	}
}

func (s *Sensor) readReg(reg uint8) (uint8, error) {
	var buf [1]byte
	if err := s.readRegs(reg, buf[:]); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func (s *Sensor) readRegs(reg uint8, out []byte) error {
	if _, err := s.file.Write([]byte{reg}); err != nil {
		return err
	}
	_, err := s.file.Read(out)
	return err
}

func (s *Sensor) writeReg(reg, value uint8) error {
	_, err := s.file.Write([]byte{reg, value})
	return err
}

func (s *Sensor) readCalibration() error {
	var raw [24]byte
	if err := s.readRegs(regCalibration, raw[:]); err != nil {
		return fmt.Errorf("чтение калибровки: %w", err)
	}

	u := func(i int) uint16 { return binary.LittleEndian.Uint16(raw[i:]) }
	i := func(n int) int16 { return int16(u(n)) }

	s.calib = calibration{
		T1: u(0), T2: i(2), T3: i(4),
		P1: u(6), P2: i(8), P3: i(10), P4: i(12), P5: i(14),
		P6: i(16), P7: i(18), P8: i(20), P9: i(22),
	}
	return nil
}

func (s *Sensor) poll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return
	}

	var raw [6]byte
	if err := s.readRegs(regData, raw[:]); err != nil {
		s.log.Error("чтение датчика", "err", err)
		return
	}

	adcP := int32(raw[0])<<12 | int32(raw[1])<<4 | int32(raw[2])>>4
	adcT := int32(raw[3])<<12 | int32(raw[4])<<4 | int32(raw[5])>>4

	tempC, tFine := s.compensateTemp(adcT)
	pressPa := s.compensatePressure(adcP, tFine)

	rec := telemetry.Record{
		DeviceID: s.cfg.ID,
		Received: time.Now(),
		Source:   s.cfg.Device,
		Data: telemetry.Telemetry{
			TempC:    tempC,
			PressPa:  pressPa,
			UptimeMS: uptimeMS(),
		},
	}

	s.state.Observe(rec)
	if err := s.buf.Append(rec); err != nil {
		s.log.Error("запись показаний своего датчика", "err", err)
	}

	// Защита получает свежие показания сразу: ждать выгрузки наверх
	// и возврата оттуда было бы бессмысленно и опасно. Кому показание
	// предназначено, решает коллекция выходов.
	s.outputs.ObserveTemp(s.cfg.ID, tempC, rec.Received)
}

// Компенсация по datasheet Bosch BMP280, раздел 3.11.3.
func (s *Sensor) compensateTemp(adc int32) (float64, float64) {
	c := s.calib
	v1 := (float64(adc)/16384.0 - float64(c.T1)/1024.0) * float64(c.T2)
	v2 := float64(adc)/131072.0 - float64(c.T1)/8192.0
	v2 = v2 * v2 * float64(c.T3)

	tFine := v1 + v2
	return tFine / 5120.0, tFine
}

func (s *Sensor) compensatePressure(adc int32, tFine float64) float64 {
	c := s.calib

	v1 := tFine/2.0 - 64000.0
	v2 := v1 * v1 * float64(c.P6) / 32768.0
	v2 += v1 * float64(c.P5) * 2.0
	v2 = v2/4.0 + float64(c.P4)*65536.0
	v1 = (float64(c.P3)*v1*v1/524288.0 + float64(c.P2)*v1) / 524288.0
	v1 = (1.0 + v1/32768.0) * float64(c.P1)

	if v1 == 0 {
		return 0 // деление на ноль при неинициализированной калибровке
	}

	p := 1048576.0 - float64(adc)
	p = (p - v2/4096.0) * 6250.0 / v1
	v1 = float64(c.P9) * p * p / 2147483648.0
	v2 = p * float64(c.P8) / 32768.0

	return p + (v1+v2+float64(c.P7))/16.0
}

var startedAt = time.Now()

// uptimeMS — время работы сборщика, чтобы его показания выглядели для
// центра так же, как показания платы.
func uptimeMS() uint32 {
	return uint32(time.Since(startedAt).Milliseconds())
}
