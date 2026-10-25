// Package rescuers — сценарии работы с карточками спасателей:
// завести, найти, поправить, показать историю тренировок.
//
// Карточка — долгоживущая запись: к ней привязаны прошлые занятия,
// поэтому карточки не удаляются, а выводятся в архив.
package rescuers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// HistoryLimit — сколько занятий показывать в карточке по умолчанию.
const HistoryLimit = 50

type Service struct {
	cards    port.CardRepository
	sessions port.SessionRepository
	log      *slog.Logger
}

func New(cards port.CardRepository, sessions port.SessionRepository, log *slog.Logger) *Service {
	return &Service{cards: cards, sessions: sessions, log: log}
}

// Create заводит карточку.
//
// author — имя из сессии администратора, а не из тела запроса: кто
// завёл карточку, не должно зависеть от того, что написал клиент.
func (s *Service) Create(ctx context.Context, rawIIN, rawName, unit, author string) (rescuer.Card, error) {
	card := rescuer.Card{
		IIN:     rescuer.NormalizeIIN(rawIIN),
		Name:    rescuer.NormalizeName(rawName),
		Unit:    trim(unit),
		Created: time.Now(),
		Author:  author,
	}
	if err := card.Validate(); err != nil {
		return rescuer.Card{}, err
	}

	if err := s.cards.Create(ctx, card); err != nil {
		if errors.Is(err, rescuer.ErrDuplicate) {
			// Говорим, чья это карточка: администратор чаще всего
			// заводит человека повторно, не найдя его в списке.
			if exist, ok, _ := s.cards.Get(ctx, card.IIN); ok {
				return rescuer.Card{}, fmt.Errorf("%w: %s", rescuer.ErrDuplicate, exist.Name)
			}
		}
		return rescuer.Card{}, err
	}

	s.log.Info("карточка заведена", "iin", card.IIN, "name", card.Name, "author", author)
	return card, nil
}

// Update меняет ФИО и подразделение. ИИН не меняется никогда: он
// опознаёт человека, и правка оторвала бы от него прошлые занятия.
func (s *Service) Update(ctx context.Context, iin rescuer.IIN, rawName, unit string) (rescuer.Card, error) {
	card, ok, err := s.cards.Get(ctx, iin)
	if err != nil {
		return rescuer.Card{}, err
	}
	if !ok {
		return rescuer.Card{}, rescuer.ErrNotFound
	}

	card.Name = rescuer.NormalizeName(rawName)
	card.Unit = trim(unit)
	if err := card.Validate(); err != nil {
		return rescuer.Card{}, err
	}
	if err := s.cards.Update(ctx, card); err != nil {
		return rescuer.Card{}, err
	}

	s.log.Info("карточка изменена", "iin", card.IIN, "name", card.Name)
	return card, nil
}

// Archive выводит карточку из обращения или возвращает обратно.
//
// Удаления нет намеренно: к карточке привязаны прошлые тренировки,
// и удаление оставило бы в архиве записи без человека.
func (s *Service) Archive(ctx context.Context, iin rescuer.IIN, archived bool) (rescuer.Card, error) {
	card, ok, err := s.cards.Get(ctx, iin)
	if err != nil {
		return rescuer.Card{}, err
	}
	if !ok {
		return rescuer.Card{}, rescuer.ErrNotFound
	}

	card.Archived = archived
	if err := s.cards.Update(ctx, card); err != nil {
		return rescuer.Card{}, err
	}

	s.log.Info("карточка переведена", "iin", card.IIN, "в архиве", archived)
	return card, nil
}

// Get возвращает карточку по ИИН.
func (s *Service) Get(ctx context.Context, iin rescuer.IIN) (rescuer.Card, bool, error) {
	return s.cards.Get(ctx, rescuer.NormalizeIIN(string(iin)))
}

// List возвращает карточки: поиск идёт по ФИО, ИИН и подразделению.
func (s *Service) List(ctx context.Context, query string, withArchived bool) ([]rescuer.Card, error) {
	return s.cards.List(ctx, trim(query), withArchived)
}

// Card — карточка вместе с историей тренировок.
type Card struct {
	Card      rescuer.Card
	Trainings []training.Session
}

// WithHistory возвращает карточку и её занятия, новые первыми.
func (s *Service) WithHistory(ctx context.Context, iin rescuer.IIN, limit int) (Card, bool, error) {
	id := rescuer.NormalizeIIN(string(iin))

	card, ok, err := s.cards.Get(ctx, id)
	if err != nil || !ok {
		return Card{}, ok, err
	}

	if limit <= 0 {
		limit = HistoryLimit
	}
	history, err := s.sessions.ByRescuer(ctx, string(id), limit)
	if err != nil {
		return Card{}, false, err
	}
	return Card{Card: card, Trainings: history}, true, nil
}

// Resolve возвращает карточки назначенных участников.
//
// Нужна при открытии занятия: показания нельзя закреплять за ИИН,
// которого нет в картотеке, а имя занятия должно браться из карточки,
// а не из того, что набрали в поле.
func (s *Service) Resolve(ctx context.Context, iins []rescuer.IIN) ([]rescuer.Card, error) {
	out := make([]rescuer.Card, 0, len(iins))
	for _, raw := range iins {
		id := rescuer.NormalizeIIN(string(raw))

		card, ok, err := s.cards.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: %s", rescuer.ErrNotFound, id)
		}
		// Архивную карточку назначать нельзя: человек выведен из
		// обращения, и новые результаты ему писать некуда.
		if card.Archived {
			return nil, fmt.Errorf("%w: %s (%s)", rescuer.ErrArchived, card.Name, id)
		}
		out = append(out, card)
	}
	return out, nil
}

func trim(s string) string {
	return string(rescuer.NormalizeName(s))
}
