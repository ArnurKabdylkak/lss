package repo

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
)

// Cards — карточки спасателей в памяти процесса.
//
// Запасной вариант, когда база не подключена: карточки переживают
// работу центра, но не его перезапуск. Об этом говорится при старте.
type Cards struct {
	mu    sync.RWMutex
	cards map[rescuer.IIN]rescuer.Card
}

func NewCards() *Cards {
	return &Cards{cards: make(map[rescuer.IIN]rescuer.Card)}
}

func (c *Cards) Create(_ context.Context, card rescuer.Card) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Повтор ИИН не допускается: карточка — это человек, и вторая
	// на того же человека разорвала бы его историю надвое.
	if _, ok := c.cards[card.IIN]; ok {
		return rescuer.ErrDuplicate
	}
	c.cards[card.IIN] = card
	return nil
}

func (c *Cards) Update(_ context.Context, card rescuer.Card) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	old, ok := c.cards[card.IIN]
	if !ok {
		return rescuer.ErrNotFound
	}
	// Кто и когда завёл карточку — не меняется правкой ФИО.
	card.Created = old.Created
	card.Author = old.Author
	c.cards[card.IIN] = card
	return nil
}

func (c *Cards) Get(_ context.Context, iin rescuer.IIN) (rescuer.Card, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	card, ok := c.cards[iin]
	return card, ok, nil
}

func (c *Cards) List(_ context.Context, query string, withArchived bool) ([]rescuer.Card, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(query))

	out := make([]rescuer.Card, 0, len(c.cards))
	for _, card := range c.cards {
		if card.Archived && !withArchived {
			continue
		}
		if q != "" && !matches(card, q) {
			continue
		}
		out = append(out, card)
	}

	// По алфавиту: список карточек читают глазами.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].IIN < out[j].IIN
	})
	return out, nil
}

func matches(c rescuer.Card, q string) bool {
	return strings.Contains(strings.ToLower(string(c.Name)), q) ||
		strings.Contains(strings.ToLower(string(c.IIN)), q) ||
		strings.Contains(strings.ToLower(c.Unit), q)
}
