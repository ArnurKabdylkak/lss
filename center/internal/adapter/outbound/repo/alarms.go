package repo

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// Alarms — журнал аварий в памяти процесса. Запасной вариант без базы.
type Alarms struct {
	mu     sync.RWMutex
	events []layout.AlarmEvent
}

func NewAlarms() *Alarms { return &Alarms{} }

func (a *Alarms) Add(_ context.Context, e layout.AlarmEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.events = append(a.events, e)
	return nil
}

func (a *Alarms) Recent(_ context.Context, limit int) ([]layout.AlarmEvent, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]layout.AlarmEvent, len(a.events))
	copy(out, a.events)
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a *Alarms) Resolve(_ context.Context, id string, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.events {
		if a.events[i].ID == id {
			a.events[i].Reset = at
			return nil
		}
	}
	return nil
}
