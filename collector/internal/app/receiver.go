package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// MaxDatagramSize — потолок датаграммы от платы, с запасом.
const MaxDatagramSize = 1024

// Receiver слушает UDP-порт объекта: принимает телеметрию от плат и
// подтверждения команд, отправляет команды обратно.
//
// Один сокет на оба направления — плата отвечает на тот адрес, с
// которого пришла команда, и отдельный сокет только запутал бы NAT
// и правила сети.
type Receiver struct {
	cfg   Config
	state *State
	buf   *DiskBuffer
	log   *slog.Logger

	// Второй источник температуры для защиты: датчик платы. Показания
	// идут по сети, поэтому учитываются только пока свежие.
	outputs *Outputs

	mu   sync.Mutex
	conn net.PacketConn
	seq  uint32

	acks chan telemetry.Ack
}

func NewReceiver(cfg Config, state *State, buf *DiskBuffer, log *slog.Logger) *Receiver {
	return &Receiver{
		cfg:   cfg,
		state: state,
		buf:   buf,
		log:   log,
		acks:  make(chan telemetry.Ack, 64),
	}
}

// Acks — подтверждения, собранные с плат; их забирает выгрузка в центр.
func (r *Receiver) Acks() <-chan telemetry.Ack { return r.acks }

// Run принимает датаграммы до отмены контекста.
func (r *Receiver) Run(ctx context.Context) error {
	conn, err := net.ListenPacket("udp", r.cfg.Listen)
	if err != nil {
		return fmt.Errorf("открытие UDP-сокета: %w", err)
	}

	r.mu.Lock()
	r.conn = conn
	r.mu.Unlock()

	r.log.Info("принимаю телеметрию", "addr", conn.LocalAddr().String())

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, MaxDatagramSize)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				r.log.Info("приём остановлен")
				return nil
			}
			r.log.Error("чтение датаграммы", "err", err)
			return err
		}
		r.handle(buf[:n], addr)
	}
}

func (r *Receiver) handle(raw []byte, addr net.Addr) {
	from, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		r.log.Warn("адрес источника не разобран", "addr", addr.String())
		return
	}

	// Плата шлёт в один сокет и телеметрию, и подтверждения команд.
	if telemetry.IsAck(raw) {
		ack, err := telemetry.ParseAck(string(raw))
		if err != nil {
			r.log.Warn("подтверждение не разобрано", "from", from, "raw", string(raw))
			return
		}
		select {
		case r.acks <- ack:
		default:
			r.log.Error("очередь подтверждений переполнена", "id", ack.ID)
		}
		return
	}

	data, err := telemetry.DecodeDatagram(raw)
	if err != nil {
		r.log.Warn("пакет не разобран", "from", from, "raw", string(raw), "err", err)
		return
	}
	if err := data.Validate(); err != nil {
		r.log.Warn("пакет отброшен", "from", from, "err", err)
		return
	}

	// Опознание: плата своего идентификатора не передаёт.
	id, known := r.cfg.DeviceID(from.Addr().String())
	if !known {
		if !r.cfg.AcceptUnknown {
			r.log.Warn("пакет с незнакомого адреса отброшен", "from", from)
			return
		}
		id = from.Addr().String()
	}

	rec := telemetry.Record{
		DeviceID: id,
		Received: time.Now(),
		Source:   from.String(),
		Data:     data,
	}

	r.state.Observe(rec)
	if err := r.buf.Append(rec); err != nil {
		r.log.Error("запись в буфер", "device", id, "err", err)
	}

	// Температура платы — второй датчик тепловой защиты. Берётся сразу
	// при приёме: ждать выгрузки в центр и возврата оттуда было бы
	// бессмысленно, решение принимается здесь.
	//
	// Кому именно предназначено показание, решает коллекция выходов: у
	// каждой пушки свой список источников, а ключи температуру
	// игнорируют.
	if data.IsSensorReading() && data.TempC != 0 {
		r.outputs.ObserveTemp(id, data.TempC, rec.Received)
	}
}

// nextSeq выдаёт номер команды для защиты платы от повтора.
//
// Нумерует сборщик, а не центр: seq — свойство канала «сборщик → плата»,
// и только сборщик видит его целиком. У центра и у самого сборщика
// (досылка уставок) независимые источники команд, и две сквозные
// нумерации для одной платы неизбежно разъехались бы — плата отвергала
// бы чужие команды как устаревшие.
//
// За основу берётся время в секундах: оно монотонно и переживает
// перезапуск обоих концов, тогда как счётчик в памяти после
// перезапуска сборщика начался бы заново и был бы отвергнут платой.
// max с предыдущим значением защищает от отката часов назад.
func (r *Receiver) nextSeq() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := uint32(time.Now().Unix())
	if now <= r.seq {
		now = r.seq + 1
	}
	r.seq = now
	return now
}

// Send отправляет команду плате.
func (r *Receiver) Send(cmd telemetry.Command) error {
	addr, port, ok := r.cfg.AddrFor(string(cmd.Device))
	if !ok {
		return fmt.Errorf("устройство %s нет в карте объекта", cmd.Device)
	}

	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()

	if conn == nil {
		return errors.New("приёмник ещё не запущен")
	}

	target, err := net.ResolveUDPAddr("udp",
		net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("адрес устройства: %w", err)
	}

	cmd.Seq = r.nextSeq()

	payload := cmd.Encode()
	if len(payload) > telemetry.MaxCommandLen {
		return fmt.Errorf("команда длиннее буфера платы: %d байт", len(payload))
	}

	if _, err := conn.WriteTo([]byte(payload), target); err != nil {
		return fmt.Errorf("отправка команды: %w", err)
	}

	r.log.Info("команда отправлена", "device", cmd.Device, "kind", cmd.Kind, "id", cmd.ID)
	return nil
}
