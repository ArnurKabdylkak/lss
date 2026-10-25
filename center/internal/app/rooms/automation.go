package rooms

import (
	"context"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// Автоматика узлов: выдержка и периодическое переключение.
//
// Два свойства узла из раскладки, за которыми следит центр, а не
// сборщик:
//
//   - hold — узел сам выключается через заданное время. Магниты
//     «Завалов» держат трубы током и, оставшись включёнными, сгорают;
//     человек же, уронив завал, занят участниками, а не пультом.
//
//   - cycle — узел переключается сам с заданным периодом, пока
//     комната готовится или идёт занятие. Так работают прожекторы:
//     ровный свет в дыму бесполезен, а мигание сбивает ориентацию.
//
// Почему это центр, а не сборщик: выдержка и мигание — часть сценария
// помещения, а сценарий живёт в одном месте. Сборщик исполняет узлы и
// не знает, зачем их включают.
//
// Ручное переключение имеет приоритет: оператор, нажавший кнопку,
// должен получить то, что нажал, а не борьбу с автоматикой. Поэтому
// после ручной команды автоматика по этому узлу молчит до конца цикла
// комнаты — возврата в «Ожидание» или новой подготовки.

// automationTick — как часто проверяются выдержки и периоды. Шаг
// заметно короче самой короткой выдержки: трёхсекундная задержка,
// проверяемая раз в секунду, растянулась бы до четырёх.
const automationTick = 250 * time.Millisecond

// RunAutomation ведёт автоматику узлов до отмены контекста.
func (s *Service) RunAutomation(ctx context.Context) {
	t := time.NewTicker(automationTick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.stepAutomation(ctx)
		// Ожидания проверяются тем же тикером: это та же забота о
		// том, чтобы показанное состояние не расходилось с залом.
		s.stepPending(ctx)
	}
}

// stepAutomation проходит по комнатам и доводит узлы до того
// состояния, которого требует раскладка.
func (s *Service) stepAutomation(ctx context.Context) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return
	}

	for _, room := range obj.Rooms {
		units := room.Units("")
		if !hasAutomation(units) {
			continue
		}

		st, err := s.State(ctx, room.ID)
		if err != nil {
			continue
		}
		// В аварии узлами распоряжается сценарий остановки, и
		// мигать прожектором поверх него нельзя.
		if st.State == layout.StateAlarm {
			continue
		}

		for _, u := range units {
			switch {
			case u.Hold > 0:
				s.holdUnit(ctx, room, st, u)
			case u.Cycle > 0:
				s.cycleUnit(ctx, room, st, u)
			}
		}
	}
}

func hasAutomation(units []layout.Unit) bool {
	for _, u := range units {
		if u.Hold > 0 || u.Cycle > 0 {
			return true
		}
	}
	return false
}

// holdUnit выключает узел, пробывший включённым дольше выдержки.
func (s *Service) holdUnit(ctx context.Context, room layout.Room,
	st layout.RoomState, u layout.Unit) {

	if !unitOn(st, u.ID) {
		return
	}
	since, ok := st.UnitSince[u.ID]
	if !ok {
		// Отметки времени нет: состояние осталось от прежней версии
		// центра. Ставим её сейчас, чтобы выдержка пошла от понятного
		// момента, а не выключала узел немедленно.
		s.touchUnit(ctx, room.ID, u.ID)
		return
	}
	if time.Since(since) < u.Hold {
		return
	}

	s.log.Info("узел выключен по выдержке",
		"room", room.ID, "узел", u.ID, "выдержка", u.Hold)
	if _, err := s.SetUnit(ctx, room.ID, u.ID, false, authorAuto); err != nil {
		s.log.Error("узел не выключен по выдержке",
			"room", room.ID, "узел", u.ID, "err", err)
	}
}

// cycleUnit переключает узел с заданным периодом.
func (s *Service) cycleUnit(ctx context.Context, room layout.Room,
	st layout.RoomState, u layout.Unit) {

	// Автоматика работает только в рабочих состояниях: в «Ожидании»
	// комната пуста, и мигать там нечему.
	if st.State != layout.StatePreparing && st.State != layout.StateTraining &&
		st.State != layout.StateReady {
		return
	}
	// Ручное переключение снимает автоматику до конца цикла комнаты.
	if st.Manual[u.ID] {
		return
	}

	since, ok := st.UnitSince[u.ID]
	if !ok {
		s.touchUnit(ctx, room.ID, u.ID)
		return
	}
	if time.Since(since) < u.Cycle {
		return
	}

	next := !unitOn(st, u.ID)
	if _, err := s.SetUnit(ctx, room.ID, u.ID, next, authorAuto); err != nil {
		s.log.Error("узел не переключён автоматикой",
			"room", room.ID, "узел", u.ID, "err", err)
	}
}

// unitOn — включён ли узел с точки зрения автоматики.
//
// Неподтверждённая команда считается исполненной: нагрузка получает
// питание в момент команды, а не в момент ответа сборщика. Иначе при
// плохой связи магнит держался бы дольше выдержки, а прожектор,
// которому не ответили, «включался» бы снова и снова.
func unitOn(st layout.RoomState, unit string) bool {
	if want, waiting := st.Pending[unit]; waiting {
		return want
	}
	return st.Equipment[unit]
}

// touchUnit отмечает узел текущим временем, не трогая его состояние.
func (s *Service) touchUnit(ctx context.Context, room layout.RoomID, unit string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.State(ctx, room)
	if err != nil {
		return
	}
	if st.UnitSince == nil {
		st.UnitSince = map[string]time.Time{}
	}
	st.UnitSince[unit] = time.Now()
	if err := s.states.Save(ctx, st); err != nil {
		s.log.Error("отметка времени узла не сохранена",
			"room", room, "узел", unit, "err", err)
	}
}

// authorAuto — кто значится автором автоматических переключений.
// Отличать их от действий человека важно для разбора: по журналу
// видно, кто погасил прожектор — оператор или выдержка.
const authorAuto = "автоматика"

// Auto сообщает, что команда пришла от автоматики, а не от человека.
func Auto(author string) bool { return author == authorAuto }

// PendingTTL — сколько центр ждёт подтверждения от сборщика.
//
// Команда уходит сборщику в ответе на его же пакет, а тот отчитывается
// следующим: при живой связи ответ приходит за секунды. Пятнадцать —
// это запас на занятую шину и медленную плату, и при этом человек не
// успевает решить, что пульт сломался.
const PendingTTL = 15 * time.Second

// stepPending снимает ожидания, на которые сборщик не ответил.
//
// Без этого потерянный ответ оставлял бы узел «ждущим» навсегда:
// лампа на пульте мигает, повторное нажатие отбрасывается, и узел
// нельзя переключить до перезапуска центра. Снятое ожидание
// возвращает узел к последнему подтверждённому состоянию — то есть к
// тому, что на самом деле известно про зал.
func (s *Service) stepPending(ctx context.Context) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return
	}

	for _, room := range obj.Rooms {
		s.expirePending(ctx, room)
	}
}

// expirePending снимает просроченные ожидания одной комнаты под
// блокировкой: между чтением и записью может прийти подтверждение.
func (s *Service) expirePending(ctx context.Context, room layout.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()

	{
		st, err := s.State(ctx, room.ID)
		if err != nil || len(st.Pending) == 0 {
			return
		}

		var stale []string
		for unit := range st.Pending {
			since, ok := st.PendingSince[unit]
			if !ok {
				// Отметки нет: ожидание пришло от прежней версии
				// центра. Ставим её сейчас, иначе срок истёк бы
				// немедленно и команда не дождалась бы ответа.
				st.PendingSince = ensureSince(st.PendingSince)
				st.PendingSince[unit] = time.Now()
				stale = append(stale, "")
				continue
			}
			if time.Since(since) >= PendingTTL {
				stale = append(stale, unit)
			}
		}
		if len(stale) == 0 {
			return
		}

		for _, unit := range stale {
			if unit == "" {
				continue
			}
			want := st.Pending[unit]
			delete(st.Pending, unit)
			delete(st.PendingSince, unit)
			s.log.Warn("сборщик не подтвердил узел за отведённое время",
				"room", room.ID, "узел", unit, "хотели включить", want,
				"срок", PendingTTL)
		}

		if err := s.states.Save(ctx, st); err != nil {
			s.log.Error("состояние после снятия ожидания не сохранено",
				"room", room.ID, "err", err)
		}
	}
}

func ensureSince(m map[string]time.Time) map[string]time.Time {
	if m == nil {
		return map[string]time.Time{}
	}
	return m
}
