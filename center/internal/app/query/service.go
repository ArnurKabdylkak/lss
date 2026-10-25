// Package query — сценарии чтения: отдать состояние и историю наружу,
// не касаясь приёмного тракта.
package query

import (
	"context"
	"errors"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// ErrNoHistory — постоянное хранилище не подключено, истории нет.
var ErrNoHistory = errors.New("история недоступна: хранилище не подключено")

type Service struct {
	repo    port.StateRepository
	history port.HistoryRepository
}

// New принимает историю необязательной: без постоянного хранилища центр
// работает, просто не отдаёт прошлое.
func New(repo port.StateRepository, history port.HistoryRepository) *Service {
	return &Service{repo: repo, history: history}
}

// Device возвращает последний пакет устройства; второе значение — найдено ли.
func (s *Service) Device(ctx context.Context, id telemetry.DeviceID) (telemetry.Packet, bool, error) {
	return s.repo.Latest(ctx, id)
}

// Devices возвращает последнее состояние всех известных устройств.
func (s *Service) Devices(ctx context.Context) ([]telemetry.Packet, error) {
	return s.repo.List(ctx)
}

// History возвращает историю устройства за интервал.
func (s *Service) History(ctx context.Context, q port.HistoryQuery) ([]telemetry.Packet, error) {
	if s.history == nil {
		return nil, ErrNoHistory
	}
	return s.history.History(ctx, q)
}

// HasHistory сообщает, подключено ли постоянное хранилище.
func (s *Service) HasHistory() bool { return s.history != nil }
