package repo

import (
	"context"
	"sort"
	"sync"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Sessions — занятия в памяти процесса.
//
// Память, а не база: тренажёр работает сменами, и до появления
// постоянного хранилища важнее, чтобы занятие открывалось без
// внешних зависимостей. Перезапуск центра закрытые занятия теряет —
// это записано в STATUS.md как долг.
type Sessions struct {
	mu     sync.RWMutex
	active map[string]training.Session   // комната -> открытое занятие
	past   map[string][]training.Session // комната -> закрытые, новые в конце
}

// MaxPastPerRoom ограничивает память: держим только последние занятия.
const MaxPastPerRoom = 100

func NewSessions() *Sessions {
	return &Sessions{
		active: make(map[string]training.Session),
		past:   make(map[string][]training.Session),
	}
}

func (s *Sessions) Save(_ context.Context, sess training.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess.Active() {
		s.active[sess.Room] = sess
		return nil
	}

	delete(s.active, sess.Room)

	// Повторное сохранение закрытого занятия заменяет запись, а не
	// добавляет вторую: иначе правка причины раздвоила бы тренировку
	// в карточке спасателя.
	past := s.past[sess.Room]
	for i := range past {
		if past[i].ID == sess.ID {
			past[i] = sess
			s.past[sess.Room] = past
			return nil
		}
	}

	past = append(past, sess)
	if len(past) > MaxPastPerRoom {
		past = past[len(past)-MaxPastPerRoom:]
	}
	s.past[sess.Room] = past
	return nil
}

func (s *Sessions) Active(_ context.Context, room string) (training.Session, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.active[room]
	return sess, ok, nil
}

// ByRescuer возвращает закрытые занятия, в которых участвовала карточка.
func (s *Sessions) ByRescuer(_ context.Context, iin string, limit int) ([]training.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []training.Session
	for _, room := range s.past {
		for _, sess := range room {
			if sess.HasRescuer(iin) {
				out = append(out, sess)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Finished.After(out[j].Finished)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Get возвращает занятие по идентификатору — открытое или закрытое.
func (s *Sessions) Get(_ context.Context, id string) (training.Session, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sess := range s.active {
		if string(sess.ID) == id {
			return sess, true, nil
		}
	}
	for _, room := range s.past {
		for _, sess := range room {
			if string(sess.ID) == id {
				return sess, true, nil
			}
		}
	}
	return training.Session{}, false, nil
}

func (s *Sessions) Recent(_ context.Context, room string, limit int) ([]training.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Пустая комната — весь объект: журнал результатов смотрят
	// целиком, а не по одной комнате.
	var past []training.Session
	if room == "" {
		for _, list := range s.past {
			past = append(past, list...)
		}
	} else {
		past = s.past[room]
	}
	out := make([]training.Session, len(past))
	copy(out, past)

	// Новые первыми: оператор смотрит на последний заезд.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Finished.After(out[j].Finished)
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
