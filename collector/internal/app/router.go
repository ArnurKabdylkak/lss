package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Router решает, кому адресована команда из центра.
//
// Разделение диктует железо: электромагниты препятствий висят на
// выводах платы, реле тепловой пушки — на выводе Raspberry Pi. Команда
// пушки, отправленная плате, просто пропала бы, поэтому развилка
// должна быть явной и в одном месте.
type Router struct {
	cfg     Config
	outputs *Outputs
	room    *Room
	send    CommandSender
	state   *State
	buf     *DiskBuffer
	log     *slog.Logger
}

func NewRouter(cfg Config, outputs *Outputs, room *Room, send CommandSender, state *State,
	buf *DiskBuffer, log *slog.Logger) *Router {

	return &Router{cfg: cfg, outputs: outputs, room: room, send: send,
		state: state, buf: buf, log: log}
}

// Dispatch исполняет команду или передаёт её плате.
//
// Второе значение — подтверждение, если команду исполнил сам сборщик.
// Для команд платы подтверждение придёт позже, от неё.
func (r *Router) Dispatch(cmd telemetry.Command) (*telemetry.Ack, error) {
	switch cmd.Kind {
	case telemetry.CmdHeaterOn, telemetry.CmdHeaterOff, telemetry.CmdSetLimits:
		ack := r.applyHeater(cmd)
		return &ack, nil

	// Команды помещения адресованы сборщику: он знает свою обвязку и
	// переводит выходы сам. Центру не нужно знать, на каком выводе
	// какое реле.
	case telemetry.CmdPrepare:
		return r.ack(cmd, r.room.Prepare(true)), nil

	case telemetry.CmdPrepareStop:
		return r.ack(cmd, r.room.Prepare(false)), nil

	case telemetry.CmdEquipment:
		return r.ack(cmd, r.room.SetUnit(cmd.Unit, cmd.State == "on")), nil

	case telemetry.CmdAlarm:
		// Авария исполняется до ответа центру: подтверждение может
		// не дойти, а зал должен быть безопасным в любом случае.
		err := r.room.Alarm()
		r.log.Error("АВАРИЙНАЯ ОСТАНОВКА помещения", "команда", cmd.ID, "err", err)
		return r.ack(cmd, err), nil

	case telemetry.CmdAlarmReset:
		// Разрешение работать, а не команда включиться: оборудование
		// само не возобновляется.
		r.room.AlarmReset()
		return r.ack(cmd, nil), nil

	case telemetry.CmdRubble:
		return r.ack(cmd, r.room.Rubble()), nil

	case telemetry.CmdDrop, telemetry.CmdArm, telemetry.CmdSetWheel,
		telemetry.CmdSetPeriod, telemetry.CmdSetCode:
		// Адресат — плата: у неё магниты и датчик колеса.
		return nil, r.send.Send(cmd)

	default:
		return nil, fmt.Errorf("неизвестная команда %q", cmd.Kind)
	}
}

// ack собирает подтверждение команды, исполненной самим сборщиком.
//
// Отказ — это тоже ответ: центр должен отличать «не исполнено, потому
// что в аварии» от потерянной команды.
func (r *Router) ack(cmd telemetry.Command, err error) *telemetry.Ack {
	ack := telemetry.Ack{ID: cmd.ID, Accepted: err == nil, Reason: "ok", UptimeMS: uptimeMS()}
	if err != nil {
		ack.Reason = err.Error()
		r.log.Warn("команда помещения не исполнена",
			"команда", cmd.Kind, "id", cmd.ID, "err", err)
		return &ack
	}

	ack.State = "off"
	if r.room != nil && r.room.State()[cmd.Unit] {
		ack.State = "on"
	}
	r.log.Info("команда помещения исполнена", "команда", cmd.Kind, "id", cmd.ID)
	return &ack
}

// applyHeater исполняет команду выхода на месте.
//
// Адресат берётся из Device: на объекте несколько реле, и на один вид
// команды их уже не разделить. Отдельные виды команд под каждое реле
// пришлось бы вносить в общий контракт при каждом новом выходе.
func (r *Router) applyHeater(cmd telemetry.Command) telemetry.Ack {
	ack := telemetry.Ack{ID: cmd.ID, Reason: "ok"}

	out, ok := r.outputs.ByID(string(cmd.Device))
	if !ok {
		// Молчаливое согласие здесь хуже отказа: оператор решил бы, что
		// команда исполнена, а реле не шевельнулось.
		ack.Reason = "unknown_output"
		ack.State = "off"
		r.log.Warn("команда адресована неизвестному выходу",
			"id", cmd.ID, "device", cmd.Device, "kind", cmd.Kind)
		return ack
	}

	var st HeaterState

	switch cmd.Kind {
	case telemetry.CmdHeaterOn:
		st = out.Allow(true)
		// Разрешение — не принуждение: у нагрева защита могла не дать
		// включить. Оператор должен видеть разницу, а не гадать.
		ack.Accepted = st.On
		if !st.On {
			ack.Reason = string(st.Reason)
		}

	case telemetry.CmdHeaterOff:
		// Обесточивание безопасно при любом состоянии.
		st = out.Allow(false)
		ack.Accepted = true

	case telemetry.CmdSetLimits:
		if err := out.SetLimits(cmd.LimitC, cmd.RestoreC); err != nil {
			st = out.State()
			ack.Reason = err.Error()
		} else {
			st = out.State()
			ack.Accepted = true
		}
	}

	ack.State = "off"
	if st.On {
		ack.State = "on"
	}
	ack.UptimeMS = uptimeMS()

	r.publishHeater(st, true)
	return ack
}

// publishHeater отдаёт состояние выхода в общий поток телеметрии.
//
// Каждый выход виден в центре как отдельное устройство: у пушки своя
// температура и своё состояние, и мешать их ни с показаниями
// велосипеда, ни между собой нельзя.
func (r *Router) publishHeater(st HeaterState, asEvent bool) {
	if st.ID == "" {
		return // выходов нет или состояние пустое
	}

	data := telemetry.Telemetry{
		State:    stateWord(st.On),
		Reason:   string(st.Reason),
		LimitC:   st.LimitC,
		UptimeMS: uptimeMS(),
	}
	if st.DecidedC != nil {
		data.TempC = *st.DecidedC
	}
	if asEvent {
		data.Event = telemetry.EventRelay
	}

	// Линию берём у самого выхода: по журналу центра должно быть видно,
	// какой именно вывод щёлкнул.
	source := st.ID
	if out, ok := r.outputs.ByID(st.ID); ok {
		source = out.Source()
	}

	rec := telemetry.Record{
		DeviceID: st.ID,
		Received: time.Now(),
		Source:   source,
		Data:     data,
	}

	r.state.Observe(rec)
	if err := r.buf.Append(rec); err != nil {
		r.log.Error("запись состояния выхода", "output", st.ID, "err", err)
	}
}

func stateWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
