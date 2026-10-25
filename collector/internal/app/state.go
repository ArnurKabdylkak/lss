package app

import (
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// State — текущее состояние объекта: последнее, что известно про каждое
// устройство. Живёт в памяти и нужен локальной панели, которая обязана
// работать без связи с центром.
type State struct {
	mu      sync.RWMutex
	devices map[string]*DeviceState
	silence time.Duration

	// Собственные устройства сборщика: пушка и свой датчик.
	//
	// Надзор за живостью для них бессмыслен — они здесь же, в этом
	// процессе, и «пропасть» не могут. Их состояние публикуется редко,
	// и без этого исключения журнал заполнялся бы ложными тревогами.
	local map[string]bool

	// Кого надо догнать уставками после перезапуска.
	rebooted chan string
}

// DeviceState — что сборщик знает об устройстве.
type DeviceState struct {
	ID       string              `json:"id"`
	Source   string              `json:"source"`
	Last     telemetry.Telemetry `json:"last"`
	Received time.Time           `json:"received"`
	Packets  uint64              `json:"packets"`
	Silent   bool                `json:"silent"`
	Reboots  uint32              `json:"reboots"`
}

func NewState(silence time.Duration, local ...string) *State {
	s := &State{
		devices:  make(map[string]*DeviceState),
		silence:  silence,
		local:    make(map[string]bool, len(local)),
		rebooted: make(chan string, 16),
	}
	for _, id := range local {
		if id != "" {
			s.local[id] = true
		}
	}
	return s
}

// Rebooted — устройства, которые перезапустились: им надо дослать уставки.
// Плата их не хранит, поэтому после перезапуска работает на значениях
// из прошивки, пока сборщик не догонит её своими.
func (s *State) Rebooted() <-chan string { return s.rebooted }

// Observe обновляет состояние принятой записью.
func (s *State) Observe(r telemetry.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.devices[r.DeviceID]
	if !ok {
		st = &DeviceState{ID: r.DeviceID}
		s.devices[r.DeviceID] = st
		// Первый пакет от платы — повод дослать настройки: сборщик мог
		// перезапуститься сам. Своим устройствам досылать нечего.
		if !s.local[r.DeviceID] {
			s.notifyReboot(r.DeviceID)
		}
	} else if r.Data.UptimeMS < st.Last.UptimeMS && !s.local[r.DeviceID] {
		st.Reboots++
		s.notifyReboot(r.DeviceID)
	}

	st.Source = r.Source
	st.Last = r.Data
	st.Received = r.Received
	st.Packets++
	st.Silent = false
}

// notifyReboot вызывается под мьютексом.
func (s *State) notifyReboot(id string) {
	select {
	case s.rebooted <- id:
	default:
	}
}

// CheckSilence помечает устройства, от которых давно нет пакетов, и
// возвращает тех, кто пропал только что.
//
// Живость плат отслеживает сборщик, а не центр: здесь виден настоящий
// поток, а наверх данные уходят пачками.
func (s *State) CheckSilence() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.silence <= 0 {
		return nil
	}

	var lost []string
	for id, st := range s.devices {
		if st.Silent || s.local[id] {
			continue
		}
		if time.Since(st.Received) > s.silence {
			st.Silent = true
			lost = append(lost, id)
		}
	}
	return lost
}

// Snapshot возвращает состояние всех устройств.
func (s *State) Snapshot() []DeviceState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]DeviceState, 0, len(s.devices))
	for _, st := range s.devices {
		out = append(out, *st)
	}
	return out
}

// Device возвращает состояние одного устройства.
func (s *State) Device(id string) (DeviceState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.devices[id]
	if !ok {
		return DeviceState{}, false
	}
	return *st, true
}
