package repo

import (
	"context"
	"sync"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// States — состояния комнат в памяти процесса.
//
// Запасной вариант, когда база не подключена. Перезапуск центра
// сбрасывает состояния в «Ожидание», и об этом говорится при старте.
type States struct {
	mu     sync.RWMutex
	states map[layout.RoomID]layout.RoomState
}

func NewStates() *States {
	return &States{states: make(map[layout.RoomID]layout.RoomState)}
}

func (s *States) Save(_ context.Context, st layout.RoomState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.states[st.Room] = st
	return nil
}

func (s *States) Get(_ context.Context, room layout.RoomID) (layout.RoomState, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.states[room]
	return st, ok, nil
}

func (s *States) All(_ context.Context) ([]layout.RoomState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]layout.RoomState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, st)
	}
	return out, nil
}
