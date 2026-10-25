// Package bcast рассылает изменения состояния подписчикам.
package bcast

import (
	"sync"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// SSE — рассылка пакетов подписчикам HTTP-потока.
//
// Панель управления должна видеть результат команды сразу: при опросе
// раз в секунду оператор жмёт кнопку и несколько мгновений не понимает,
// сработала ли она.
type SSE struct {
	mu   sync.RWMutex
	subs map[int]chan telemetry.Packet
	next int

	// Ёмкость очереди подписчика. Медленный подписчик пропускает
	// пакеты, но не тормозит приёмный тракт.
	Buffer int
}

func NewSSE() *SSE {
	return &SSE{subs: make(map[int]chan telemetry.Packet), Buffer: 32}
}

// Subscribe возвращает канал пакетов и функцию отписки.
func (s *SSE) Subscribe() (<-chan telemetry.Packet, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.next
	s.next++
	ch := make(chan telemetry.Packet, s.Buffer)
	s.subs[id] = ch

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if ch, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(ch)
		}
	}
}

// Publish рассылает пакет подписчикам.
//
// Отправка неблокирующая: приём телеметрии не должен зависеть от того,
// успевает ли читать браузер оператора.
func (s *SSE) Publish(p telemetry.Packet) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, ch := range s.subs {
		select {
		case ch <- p:
		default:
		}
	}
}

// Subscribers сообщает число подписчиков — для диагностики.
func (s *SSE) Subscribers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subs)
}
