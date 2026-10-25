package rooms

import (
	"context"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// Секция как одно помещение.
//
// Дымокамера — два этажа, но для занятия это одна комната: звено
// проходит их подряд, таймер, видеозапись и зачёт у него общие
// (сценарий дымокамеры §3). Поэтому состав записывают один раз,
// подготовку запускают один раз, и смена закрывается тоже разом.
//
// Разделение на комнаты остаётся там, где оно настоящее: у каждого
// этажа свой сборщик, свои реле и свои камеры. Операции, которые
// касаются занятия, а не железа, расходятся по этажам сами — оператор
// о двух комнатах не думает.

// SetRosterSection записывает состав на всю секцию.
func (s *Service) SetRosterSection(ctx context.Context, id layout.RoomID,
	team string, rescuers []string, author string) (layout.RoomState, error) {

	return s.fanOut(ctx, id, func(room layout.RoomID) (layout.RoomState, error) {
		return s.SetRoster(ctx, room, team, rescuers, author)
	})
}

// StartPreparationSection запускает подготовку всей секции.
func (s *Service) StartPreparationSection(ctx context.Context, id layout.RoomID,
	author string) (layout.RoomState, error) {

	return s.fanOut(ctx, id, func(room layout.RoomID) (layout.RoomState, error) {
		return s.StartPreparation(ctx, room, author)
	})
}

// SetStateSection переводит всю секцию в новое состояние.
func (s *Service) SetStateSection(ctx context.Context, id layout.RoomID,
	to layout.State, reason, author string) (layout.RoomState, error) {

	return s.fanOut(ctx, id, func(room layout.RoomID) (layout.RoomState, error) {
		return s.SetState(ctx, room, to, reason, author)
	})
}

// fanOut выполняет действие над всеми комнатами секции и возвращает
// состояние той, с которой начали.
//
// Ошибка любой комнаты прекращает обход: половина подготовленной
// дымокамеры — это зал, в котором одна часть ждёт людей, а другая
// нет, и разбираться с этим пришлось бы вручную.
func (s *Service) fanOut(ctx context.Context, id layout.RoomID,
	do func(layout.RoomID) (layout.RoomState, error)) (layout.RoomState, error) {

	rooms, err := s.sectionRooms(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}

	var first layout.RoomState
	for i, room := range rooms {
		st, err := do(room)
		if err != nil {
			return layout.RoomState{}, err
		}
		if i == 0 {
			first = st
		}
	}
	return first, nil
}

// sectionRooms — комнаты секции, начиная с той, которую назвали.
//
// Названная идёт первой намеренно: её состояние возвращается наружу,
// и панель показывает то помещение, которое открыл оператор.
func (s *Service) sectionRooms(ctx context.Context, id layout.RoomID) ([]layout.RoomID, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return nil, err
	}

	section := obj.SectionOf(id)
	if len(section.Rooms) < 2 {
		return []layout.RoomID{id}, nil
	}

	out := make([]layout.RoomID, 0, len(section.Rooms))
	out = append(out, id)
	for _, r := range section.Rooms {
		if r != id {
			out = append(out, r)
		}
	}
	return out, nil
}
