package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Tasks — периодические работы сборщика: надзор за живостью плат,
// сброс буфера на диск, досылка уставок перезапустившимся устройствам.
//
// Всё это не требует отдельного планировщика с расписанием: работы
// периодические, короткие и их немного.
type Tasks struct {
	cfg    Config
	state  *State
	buf    *DiskBuffer
	send   CommandSender
	uplink *Uplink
	log    *slog.Logger

	// Выходы и маршрутизатор: пересчёт тепловой защиты по таймеру и
	// публикация состояния наверх. Могут быть nil, если выходов нет.
	outputs *Outputs
	router  *Router
}

func NewTasks(cfg Config, state *State, buf *DiskBuffer, send CommandSender,
	uplink *Uplink, log *slog.Logger) *Tasks {

	return &Tasks{cfg: cfg, state: state, buf: buf, send: send, uplink: uplink, log: log}
}

// Run крутит периодические работы до отмены контекста.
func (t *Tasks) Run(ctx context.Context, acks <-chan telemetry.Ack) {
	silence := time.NewTicker(time.Second)
	defer silence.Stop()

	// Сброс на диск — раз в секунду, а не на каждый пакет: fsync
	// 5 раз в секунду круглосуточно изнашивает носитель, а цена
	// отказа — потеря последней секунды телеметрии.
	flush := time.NewTicker(time.Second)
	defer flush.Stop()

	// Защита пересчитывается чаще, чем идут отчёты: реакция на
	// перегрев не должна ждать очередной выгрузки.
	protect := time.NewTicker(time.Second)
	defer protect.Stop()

	// Состояние отслеживается по каждому выходу отдельно: смена
	// состояния одной пушки не должна выглядеть как смена у всех.
	type outputReport struct {
		on          bool
		reason      HeaterReason
		sinceReport time.Duration
		seen        bool
	}
	reports := make(map[string]*outputReport, t.outputs.Len())

	for {
		select {
		case <-ctx.Done():
			if err := t.buf.Flush(); err != nil {
				t.log.Error("сброс буфера при остановке", "err", err)
			}
			return

		case <-silence.C:
			for _, id := range t.state.CheckSilence() {
				t.log.Warn("телеметрия пропала", "device", id)
			}

		case <-flush.C:
			if err := t.buf.Flush(); err != nil {
				t.log.Error("сброс буфера", "err", err)
			}

		case <-protect.C:
			if t.outputs == nil || t.router == nil {
				continue
			}

			for _, st := range t.outputs.UpdateAll() {
				rep := reports[st.ID]
				if rep == nil {
					rep = &outputReport{}
					reports[st.ID] = rep
				}

				// Срабатывание защиты — событие, и оно уходит наверх
				// сразу. Ждать очередного отчёта нельзя: инструктор
				// должен видеть, что нагрев снят, а не узнавать об этом
				// через пять секунд.
				if !rep.seen || st.On != rep.on || st.Reason != rep.reason {
					rep.seen, rep.on, rep.reason = true, st.On, st.Reason
					rep.sinceReport = 0
					t.router.publishHeater(st, true)
					continue
				}

				// Ровное состояние повторяется раз в пять секунд: оно
				// меняется редко, а панели нужно знать, что выход на
				// связи.
				rep.sinceReport += time.Second
				if rep.sinceReport >= 5*time.Second {
					rep.sinceReport = 0
					t.router.publishHeater(st, false)
				}
			}

		case id := <-t.state.Rebooted():
			t.resendLimits(id)

		case ack := <-acks:
			// Подтверждение уходит наверх: центру нужно знать исход
			// команды, а плата отвечает только сюда.
			t.uplink.QueueAck(ack)
			if ack.Accepted {
				t.log.Info("команда исполнена платой", "id", ack.ID, "state", ack.State)
			} else {
				t.log.Warn("плата отклонила команду", "id", ack.ID, "reason", ack.Reason)
			}
		}
	}
}

// resendLimits досылает плате её настройки после перезапуска.
//
// Плата их не хранит: держать настройки во флеш дорого и опасно при
// отключении питания. Вместо этого держатель конфигурации — сборщик.
// Ошибка в длине окружности искажает скорость всего заезда, поэтому
// досылка обязательна, а не «хорошо бы».
func (t *Tasks) resendLimits(id string) {
	limits, ok := t.cfg.LimitsFor(id)
	if !ok {
		return
	}

	t.log.Info("устройство перезапустилось, досылаю настройки", "device", id)

	var cmds []telemetry.Command
	if limits.WheelMM > 0 {
		cmds = append(cmds, telemetry.Command{
			ID:      t.nextID("whl"),
			Device:  telemetry.DeviceID(id),
			Kind:    telemetry.CmdSetWheel,
			WheelMM: limits.WheelMM,
		})
	}
	if limits.PeriodMS > 0 {
		cmds = append(cmds, telemetry.Command{
			ID:       t.nextID("per"),
			Device:   telemetry.DeviceID(id),
			Kind:     telemetry.CmdSetPeriod,
			PeriodMS: limits.PeriodMS,
		})
	}

	for _, c := range cmds {
		if err := c.Validate(); err != nil {
			t.log.Error("уставки из конфигурации негодны", "device", id, "err", err)
			continue
		}
		if err := t.send.Send(c); err != nil {
			t.log.Error("досылка уставок", "device", id, "err", err)
		}
	}
}

func (t *Tasks) nextID(prefix string) string {
	return prefix + "-" + newBatchID()[:6]
}
