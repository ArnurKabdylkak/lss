// Package repo содержит реализации хранилищ состояния.
package repo

import (
	"context"
	"sort"
	"sync"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Memory — хранилище последнего состояния в памяти процесса.
// Состояние нужно на каждый запрос панели, поэтому оно живёт в памяти
// даже когда подключено постоянное хранилище; при старте центра его
// восстанавливают из БД.
type Memory struct {
	mu      sync.RWMutex
	devices map[telemetry.DeviceID]telemetry.Packet
	// events — последнее событие устройства (тушение очага, ввод
	// кода). Хранится отдельно: плата следом шлёт обычный отчёт о
	// себе, и в devices событие затиралось бы через секунду.
	events map[telemetry.DeviceID]telemetry.Packet
}

func NewMemory() *Memory {
	return &Memory{devices: make(map[telemetry.DeviceID]telemetry.Packet)}
}

func (m *Memory) Save(_ context.Context, p telemetry.Packet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[p.Device] = p
	if p.Data.Event != "" {
		if m.events == nil {
			m.events = map[telemetry.DeviceID]telemetry.Packet{}
		}
		m.events[p.Device] = p
	}
	return nil
}

func (m *Memory) Latest(_ context.Context, id telemetry.DeviceID) (telemetry.Packet, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.devices[id]
	return p, ok, nil
}

func (m *Memory) List(_ context.Context) ([]telemetry.Packet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]telemetry.Packet, 0, len(m.devices))
	for _, p := range m.devices {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Device < out[j].Device
	})
	return out, nil
}

// LatestEvent возвращает последнее событие устройства — пакет с
// заполненным Event, а не просто последний пакет.
func (m *Memory) LatestEvent(_ context.Context, id telemetry.DeviceID) (telemetry.Packet, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.events[id]
	return p, ok, nil
}
