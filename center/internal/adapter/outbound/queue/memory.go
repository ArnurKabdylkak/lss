// Package queue содержит реализации очереди команд.
package queue

import (
	"context"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Memory — очередь команд в памяти процесса.
//
// Команда живёт минуты: её забирает ближайшее обращение сборщика.
// Переживать перезапуск центра ей не нужно — невыполненную команду
// оператор отдаст заново, а вот «догнавшая» через сутки после
// перезапуска команда управления нагрузкой опасна.
type Memory struct {
	mu    sync.Mutex
	sites map[string][]*port.CommandState
	byID  map[string]*port.CommandState

	// Сколько ждать подтверждения, прежде чем выдать команду снова.
	Retry time.Duration
	// Сколько хранить исполненные команды для показа в панели.
	Keep time.Duration
}

func NewMemory() *Memory {
	return &Memory{
		sites: make(map[string][]*port.CommandState),
		byID:  make(map[string]*port.CommandState),
		Retry: 10 * time.Second,
		Keep:  10 * time.Minute,
	}
}

func (m *Memory) Enqueue(_ context.Context, site string, cmd telemetry.Command) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// Новая команда тому же узлу вытесняет прежние, ещё не
	// подтверждённые. Иначе повторная выдача старой «включить»
	// приходила к сборщику после свежей «выключить» и включала узел
	// обратно: побеждала не последняя команда, а последняя доставленная.
	if key, ok := target(cmd); ok {
		for _, old := range m.sites[site] {
			if old.Delivered() {
				continue
			}
			if k, ok := target(old.Command); ok && k == key {
				old.Ack = &telemetry.Ack{ID: old.Command.ID, Reason: "superseded"}
				old.Settled = now
			}
		}
	}

	st := &port.CommandState{Command: cmd, Issued: now}
	m.sites[site] = append(m.sites[site], st)
	m.byID[cmd.ID] = st
	return nil
}

// Take отдаёт команды, которые ещё не подтверждены и не выданы только что.
func (m *Memory) Take(_ context.Context, site string, limit int,
	allow func(telemetry.Command) bool) ([]telemetry.Command, error) {

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	out := make([]telemetry.Command, 0, limit)

	for _, st := range m.sites[site] {
		if len(out) == limit {
			break
		}
		if st.Delivered() {
			continue
		}
		// Команда чужого сборщика: он её и заберёт своим опросом.
		if allow != nil && !allow(st.Command) {
			continue
		}
		// Повторная выдача — только если ответа нет дольше Retry:
		// иначе каждое обращение сборщика плодило бы дубли в эфире.
		if !st.Taken.IsZero() && now.Sub(st.Taken) < m.Retry {
			continue
		}
		st.Taken = now
		st.Attempts++
		out = append(out, st.Command)
	}

	m.cleanup(site, now)
	return out, nil
}

func (m *Memory) Settle(_ context.Context, ack telemetry.Ack) (telemetry.Command, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.byID[ack.ID]
	if !ok {
		// Подтверждение команды, о которой центр не знает: например,
		// выданной до его перезапуска. Не ошибка — просто нечего закрывать.
		return telemetry.Command{}, false, nil
	}
	copyAck := ack
	st.Ack = &copyAck
	st.Settled = time.Now()
	return st.Command, true, nil
}

func (m *Memory) Pending(_ context.Context, site string) ([]port.CommandState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]port.CommandState, 0, len(m.sites[site]))
	for _, st := range m.sites[site] {
		out = append(out, *st)
	}
	return out, nil
}

// cleanup убирает давно закрытые команды. Вызывается под мьютексом.
func (m *Memory) cleanup(site string, now time.Time) {
	kept := m.sites[site][:0]
	for _, st := range m.sites[site] {
		if st.Delivered() && now.Sub(st.Settled) > m.Keep {
			delete(m.byID, st.Command.ID)
			continue
		}
		kept = append(kept, st)
	}
	m.sites[site] = kept
}

// target — что именно переключает команда. Команды одной цели
// взаимоисключающие: исполниться должна только последняя.
func target(cmd telemetry.Command) (string, bool) {
	switch cmd.Kind {
	case telemetry.CmdEquipment:
		return string(cmd.Device) + "/" + cmd.Unit, true
	case telemetry.CmdHeaterOn, telemetry.CmdHeaterOff:
		return string(cmd.Device) + "/heater", true
	case telemetry.CmdFire:
		return string(cmd.Device) + "/fire", true
	}
	return "", false
}
