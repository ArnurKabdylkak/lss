package repo

import (
	"encoding/json"
	"fmt"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Снимок хранилищ в памяти: то, что нужно сохранить между запусками.
//
// Нужен настольной сборке для Windows. Там центр ставят одним файлом,
// и поднимать рядом отдельную базу неуместно: оператор включает
// программу, а не администрирует сервер. На сервере с MongoDB этот
// путь не используется — там хранилище настоящее.
//
// Формат — JSON: данных немного (смены за день, карточки, журнал
// аварий), а читаемый файл можно открыть глазами, когда разбираются,
// почему занятие не закрылось.

// Snapshot — всё, что хранится в памяти и переживает перезапуск.
type Snapshot struct {
	Sessions SessionsSnapshot    `json:"sessions"`
	Cards    []rescuer.Card      `json:"cards"`
	States   []layout.RoomState  `json:"states"`
	Alarms   []layout.AlarmEvent `json:"alarms"`
}

// SessionsSnapshot — открытые и закрытые занятия по комнатам.
type SessionsSnapshot struct {
	Active map[string]training.Session   `json:"active"`
	Past   map[string][]training.Session `json:"past"`
}

// Snapshot отдаёт копию занятий.
func (s *Sessions) Snapshot() SessionsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := SessionsSnapshot{
		Active: make(map[string]training.Session, len(s.active)),
		Past:   make(map[string][]training.Session, len(s.past)),
	}
	for room, sess := range s.active {
		out.Active[room] = sess
	}
	for room, list := range s.past {
		out.Past[room] = append([]training.Session(nil), list...)
	}
	return out
}

// Restore заменяет содержимое занятий снимком.
func (s *Sessions) Restore(snap SessionsSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.active = make(map[string]training.Session, len(snap.Active))
	for room, sess := range snap.Active {
		s.active[room] = sess
	}
	s.past = make(map[string][]training.Session, len(snap.Past))
	for room, list := range snap.Past {
		s.past[room] = append([]training.Session(nil), list...)
	}
}

// Snapshot отдаёт копию карточек.
func (c *Cards) Snapshot() []rescuer.Card {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]rescuer.Card, 0, len(c.cards))
	for _, card := range c.cards {
		out = append(out, card)
	}
	return out
}

// Restore заменяет картотеку снимком.
func (c *Cards) Restore(cards []rescuer.Card) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cards = make(map[rescuer.IIN]rescuer.Card, len(cards))
	for _, card := range cards {
		c.cards[card.IIN] = card
	}
}

// Snapshot отдаёт копию состояний комнат.
func (s *States) Snapshot() []layout.RoomState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]layout.RoomState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, st)
	}
	return out
}

// Restore заменяет состояния комнат снимком.
func (s *States) Restore(states []layout.RoomState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.states = make(map[layout.RoomID]layout.RoomState, len(states))
	for _, st := range states {
		s.states[st.Room] = st
	}
}

// Snapshot отдаёт копию журнала аварий.
func (a *Alarms) Snapshot() []layout.AlarmEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return append([]layout.AlarmEvent(nil), a.events...)
}

// Restore заменяет журнал аварий снимком.
func (a *Alarms) Restore(events []layout.AlarmEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.events = append([]layout.AlarmEvent(nil), events...)
}

// Encode собирает снимок в JSON.
func (s Snapshot) Encode() ([]byte, error) {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("снимок хранилища: %w", err)
	}
	return raw, nil
}

// DecodeSnapshot разбирает снимок.
func DecodeSnapshot(raw []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return Snapshot{}, fmt.Errorf("разбор снимка хранилища: %w", err)
	}
	return s, nil
}
