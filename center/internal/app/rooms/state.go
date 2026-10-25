package rooms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Состояния комнат и аварийная остановка.
//
// Состояние хранится явно, а не выводится из показаний: «Готова»
// подтверждает администратор глазами, и вывести это из данных нельзя.
// Исключение — «Тренировка»: она ровно столько, сколько открыто
// занятие, и расхождение здесь означало бы, что экран врёт.

// Fault — неисправность, которую видно на главном экране.
type Fault struct {
	Room   layout.RoomID
	Device string // устройство или камера; пусто — про комнату целиком
	Text   string
}

// StartPreparation запускает подготовку комнаты: переводит её в
// «Подготовку», включает нагрев и заводит таймер.
//
// Светильники и вытяжка по сценарию включаются здесь же; команд на них
// пока нет — оборудование комнат заводится отдельно, и до тех пор это
// делается на месте.
func (s *Service) StartPreparation(ctx context.Context, id layout.RoomID,
	author string) (layout.RoomState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok, err := s.room(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}
	if !ok {
		return layout.RoomState{}, fmt.Errorf("комната %s неизвестна", id)
	}

	current, err := s.State(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}
	if err := current.State.Move(layout.StatePreparing); err != nil {
		return layout.RoomState{}, err
	}

	// Состав записывают до подготовки: прогрев и дым стоят времени и
	// ресурса, и включать их, не зная, кто придёт, незачем. Заодно
	// это снимает спешку с ввода ФИО перед самым заходом.
	if !current.Roster.Set() {
		return layout.RoomState{}, ErrNoRoster
	}

	now := time.Now()
	prep := layout.Preparation{TargetC: room.Prepare.TargetC, Started: now}
	if room.Prepare.Timeout > 0 {
		prep.Until = now.Add(room.Prepare.Timeout)
	}

	next := layout.RoomState{
		Room:   id,
		State:  layout.StatePreparing,
		Since:  now,
		Reason: "старт подготовки",
		Author: author,
		Prep:   prep,
		Roster: current.Roster,
		// Состояние узлов задаёт сценарий комнаты из раскладки, а не
		// сборщик: сценарий должен жить в одном месте, иначе панель
		// показывает одно, а зал делает другое. Но сценарий — это
		// намерение: включённым узел считается, когда сборщик
		// отчитается, что реле щёлкнуло.
		Equipment: current.Equipment,
		Pending:   scenario(room, false),
		// Срок ожидания идёт с момента отправки команд, иначе узлы,
		// которым сборщик не ответил, зависли бы в ожидании.
		PendingSince: unitSince(room, time.Now()),
		// Новый цикл комнаты: прежние ручные переключения забываются,
		// иначе прожектор, погашенный на прошлой смене, так и не
		// замигал бы снова.
		UnitSince: unitSince(room, time.Now()),
	}

	// Каждому узлу — своя команда: сборщик исполняет, но не решает.
	if s.units != nil {
		for _, u := range room.Units("") {
			if err := s.units(ctx, room, u.ID, u.Prepare, author); err != nil {
				s.log.Error("узел не переключён при подготовке",
					"room", id, "узел", u.ID, "err", err)
			}
		}
	}

	// Код задания «Набор чисел» формируется заново на каждую
	// тренировку: иначе следующая команда пришла бы с подсказкой от
	// предыдущей. Администратор видит его до старта.
	if dev, ok := codePanel(room); ok {
		next.Code = newCode()
		if s.codes != nil {
			if err := s.codes(ctx, room, dev, next.Code, author); err != nil {
				s.log.Error("код задания не отправлен на панель",
					"room", id, "device", dev, "err", err)
			}
		}
		s.log.Info("код задания сформирован", "room", id, "device", dev)
	}

	// Очаги зажигаются подготовкой: потушенный на прошлом заходе, сам
	// он не возвращается — иначе вспыхивал бы за спиной у звена,
	// которое его уже потушило.
	if s.fires != nil {
		for _, dev := range fires(room) {
			if err := s.fires(ctx, room, dev, true, author); err != nil {
				s.log.Error("очаг не зажжён",
					"room", id, "device", dev, "err", err)
			}
		}
	}
	if err := s.states.Save(ctx, next); err != nil {
		return layout.RoomState{}, err
	}

	// Оборудование подготовки включает сборщик комнаты: состав у
	// теплокамеры и дымокамеры разный, и знать его должен тот, к кому
	// оно подключено.
	if s.prepares != nil {
		if err := s.prepares(ctx, room, author, true); err != nil {
			// Подготовка уже началась: таймер идёт, температура видна.
			// Молчать об этом нельзя, но и откатывать состояние хуже.
			s.log.Error("оборудование подготовки не включилось", "room", id, "err", err)
		}
	}

	// Нагрев разрешается отдельной командой: снимает его сборщик при
	// перегреве, что бы ни прислал центр (§7.4).
	if room.Heater.Set() && s.commands != nil {
		if err := s.commands(ctx, room, author); err != nil {
			s.log.Error("нагрев не включился командой",
				"room", id, "device", room.Heater.Device, "err", err)
		}
	}

	s.log.Info("старт подготовки",
		"room", id, "цель", prep.TargetC, "до", prep.Until.Format("15:04:05"), "кто", author)
	return next, nil
}

// SetState переводит комнату в новое состояние.
func (s *Service) SetState(ctx context.Context, room layout.RoomID,
	to layout.State, reason, author string) (layout.RoomState, error) {

	if _, ok, err := s.room(ctx, room); err != nil {
		return layout.RoomState{}, err
	} else if !ok {
		return layout.RoomState{}, fmt.Errorf("комната %s неизвестна", room)
	}

	current, err := s.State(ctx, room)
	if err != nil {
		return layout.RoomState{}, err
	}
	if err := current.State.Move(to); err != nil {
		return layout.RoomState{}, err
	}

	// «Тренировка» наступает открытием занятия, а не переключателем:
	// иначе экран показывал бы тренировку, которой нет в журнале.
	if to == layout.StateTraining {
		if _, ok, err := s.sessions.Active(ctx, string(room)); err != nil {
			return layout.RoomState{}, err
		} else if !ok {
			return layout.RoomState{}, fmt.Errorf(
				"%w: в «Тренировку» комната переходит открытием занятия", layout.ErrBadTransition)
		}
	}

	return s.move(ctx, room, to, reason, author)
}

// move записывает переход, уже проверенный вызывающим.
func (s *Service) move(ctx context.Context, room layout.RoomID,
	to layout.State, reason, author string) (layout.RoomState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.State(ctx, room)
	if err != nil {
		return layout.RoomState{}, err
	}
	if err := current.State.Move(to); err != nil {
		return layout.RoomState{}, err
	}

	next := layout.RoomState{
		Room:   room,
		State:  to,
		Since:  time.Now(),
		Reason: reason,
		Author: author,
		// Подготовка и код относятся к текущему циклу комнаты, а не
		// к состоянию: при переходе «Подготовка → Готова» они должны
		// остаться, иначе экран теряет прогрев, а занятие — код.
		Prep:         current.Prep,
		Code:         current.Code,
		Roster:       current.Roster,
		Equipment:    current.Equipment,
		Pending:      current.Pending,
		PendingSince: current.PendingSince,
		UnitSince:    current.UnitSince,
		Manual:       current.Manual,
	}
	// Возврат в «Ожидание» начинает цикл заново: прошлый прогрев, код
	// и состав следующей группе не принадлежат.
	if to == layout.StateWaiting {
		next.Prep = layout.Preparation{}
		next.Code = ""
		next.Roster = layout.Roster{}
		next.Equipment = nil
		next.Pending = nil
		next.PendingSince = nil
		next.UnitSince = nil
		next.Manual = nil
	}

	if err := s.states.Save(ctx, next); err != nil {
		return layout.RoomState{}, err
	}

	// Возврат в «Ожидание» гасит оборудование подготовки: комната
	// освободилась, и дымогенератор с пушкой работать не должны.
	if to == layout.StateWaiting && current.State != layout.StateWaiting && s.prepares != nil {
		if r, ok, err := s.room(ctx, room); err == nil && ok {
			if err := s.prepares(ctx, r, author, false); err != nil {
				s.log.Error("оборудование подготовки не выключилось", "room", room, "err", err)
			}
		}
	}

	// Выход из аварии: оборудованию разрешается работа заново.
	// Само оно не возобновляется — повторный запуск делает
	// администратор, проверив готовность (сценарий §7).
	if current.Alarm() && to != layout.StateAlarm && s.alarms != nil {
		if r, ok, err := s.room(ctx, room); err == nil && ok {
			if err := s.alarms(ctx, r, author, false); err != nil {
				s.log.Error("команда снятия аварии не ушла", "room", room, "err", err)
			}
		}
		s.resolveAlarm(ctx)
	}

	s.log.Info("состояние комнаты изменено",
		"room", room, "состояние", to.Title(), "причина", reason, "кто", author)
	return next, nil
}

// trackPreparation отмечает достижение цели.
//
// Проверяется при каждом чтении состояния: отдельный опрос завёл бы
// второй источник правды о температуре, а показания и так приходят
// в центр непрерывно.
func (s *Service) trackPreparation(ctx context.Context, st layout.RoomState,
	tempC float64, hasTemp bool) layout.RoomState {

	if st.State != layout.StatePreparing || !st.Prep.Running() {
		return st
	}
	if st.Prep.Done() || st.Prep.TargetC <= 0 || !hasTemp {
		return st
	}
	if tempC < st.Prep.TargetC {
		return st
	}

	// Отметку ставим по свежей записи: st мог устареть, пока
	// собиралось представление комнаты.
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh, err := s.State(ctx, st.Room)
	if err != nil || fresh.State != layout.StatePreparing || fresh.Prep.Done() {
		return fresh
	}
	st = fresh

	// Цель достигнута. Статус «Готова» при этом не выставляется:
	// его подтверждает администратор, осмотрев комнату (сценарий §3).
	st.Prep.Reached = time.Now()
	if err := s.states.Save(ctx, st); err != nil {
		s.log.Error("отметка о достижении цели не сохранена", "room", st.Room, "err", err)
		return st
	}

	s.log.Info("комната прогрета",
		"room", st.Room, "цель", st.Prep.TargetC, "температура", tempC,
		"за", time.Since(st.Prep.Started).Round(time.Second))
	return st
}

// State возвращает состояние комнаты.
//
// Пока комната не переводилась, она в «Ожидании» — это умолчание, а
// не запись в базе: свежеразвёрнутый центр не должен требовать
// заполнения состояний.
func (s *Service) State(ctx context.Context, room layout.RoomID) (layout.RoomState, error) {
	st, ok, err := s.states.Get(ctx, room)
	if err != nil {
		return layout.RoomState{}, err
	}
	if !ok {
		return layout.RoomState{Room: room, State: layout.StateWaiting}, nil
	}
	return st, nil
}

// Alarm объявляет аварийную остановку во всех комнатах.
//
// Это дублирование аппаратной петли, а не замена ей: нагрузку снимает
// реле безопасности, а центр останавливает учёт, закрывает занятия и
// ставит отметку в карточках (сценарий §7).
func (s *Service) Alarm(ctx context.Context, reason, author string) ([]layout.RoomState, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		reason = AlarmMark
	}

	event := layout.AlarmEvent{ID: newID(), At: time.Now(), Reason: reason, Author: author}

	// Открытые занятия закрываются с отметкой до блокировки комнат:
	// закрытие само переводит комнату и взяло бы ту же блокировку —
	// авария повисла бы, так и не обесточив зал.
	for _, room := range obj.Rooms {
		if sess, ok, err := s.sessions.Active(ctx, string(room.ID)); err != nil {
			s.log.Error("занятие при аварии не найдено", "room", room.ID, "err", err)
		} else if ok {
			if err := s.stopSession(ctx, room.ID, reason, true); err != nil {
				s.log.Error("занятие при аварии не закрыто", "room", room.ID, "err", err)
			}
			event.Sessions = append(event.Sessions, string(sess.ID))
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]layout.RoomState, 0, len(obj.Rooms))
	for _, room := range obj.Rooms {
		current, err := s.State(ctx, room.ID)
		if err != nil {
			return nil, err
		}

		// Оборудование переводится в безопасное состояние командой
		// сборщику. Отказ команды аварию не отменяет: нагрузку в любом
		// случае снимает аппаратная петля, а молчать об этом нельзя.
		if s.alarms != nil {
			if err := s.alarms(ctx, room, author, true); err != nil {
				s.log.Error("команда аварийной остановки не ушла",
					"room", room.ID, "err", err)
			}
		}

		// Что должно гореть и работать после обесточивания, решает
		// центр: в теплокамере остаётся свет и пускается вытяжка, в
		// дымокамере включаются и свет, и вытяжка. Сборщик сценария
		// не знает.
		if s.units != nil {
			for _, u := range room.Units("") {
				if u.AlarmKeep() {
					continue
				}
				if err := s.units(ctx, room, u.ID, u.AlarmOn(), author); err != nil {
					s.log.Error("узел не переведён в аварийное состояние",
						"room", room.ID, "узел", u.ID, "err", err)
				}
			}
		}

		// Очаги гаснут по аварии. Команда идёт самой плате очага:
		// её реле не на сборщике, и обесточивание помещения до неё
		// не доходит — очаг продолжал бы гореть в зале, из которого
		// людей выводят.
		//
		// Отказ не останавливает аварию: очаг — не главная опасность,
		// а ждать ответа платы, пока зал стоит под нагрузкой, нельзя.
		if s.fires != nil {
			for _, dev := range fires(room) {
				if err := s.fires(ctx, room, dev, false, author); err != nil {
					s.log.Error("очаг не потушен по аварии",
						"room", room.ID, "device", dev, "err", err)
				}
			}
		}

		// Оповещение зала. Играет центр: записи и акустика у него,
		// сборщик о звуке не знает. Запуск не держит остановку —
		// обесточивание важнее, чем фраза, и ждать её нельзя.
		if s.actions != nil {
			if err := s.actions.Announce(ctx, room, telemetry.SoundAlarm, author); err != nil {
				s.log.Error("аварийное оповещение не прозвучало",
					"room", room.ID, "err", err)
			}
		}

		st := layout.RoomState{
			Room:         room.ID,
			State:        layout.StateAlarm,
			Since:        time.Now(),
			Reason:       reason,
			Author:       author,
			Equipment:    current.Equipment,
			Pending:      alarmPending(room),
			PendingSince: unitSince(room, time.Now()),
			UnitSince:    unitSince(room, time.Now()),
		}
		if err := s.states.Save(ctx, st); err != nil {
			return nil, err
		}
		out = append(out, st)
		event.Rooms = append(event.Rooms, room.ID)
	}

	// Событие фиксируется в журнале: состояние показывает, как сейчас,
	// а разбор требует знать, когда и почему (сценарий §7).
	if s.journal != nil {
		if err := s.journal.Add(ctx, event); err != nil {
			s.log.Error("авария не записана в журнал", "err", err)
		}
	}

	s.log.Error("АВАРИЙНАЯ ОСТАНОВКА", "комнат", len(out),
		"занятий прервано", len(event.Sessions), "причина", reason, "кто", author)
	return out, nil
}

// Roster возвращает состав, записанный на предстоящую тренировку.
func (s *Service) Roster(ctx context.Context, id layout.RoomID) (layout.Roster, error) {
	st, err := s.State(ctx, id)
	if err != nil {
		return layout.Roster{}, err
	}
	return st.Roster, nil
}

// UnitCommands — включение и выключение узла оборудования помещения.
// Адресуется сборщику: какое реле за что отвечает, знает он.
type UnitCommands func(ctx context.Context, room layout.Room, unit string, on bool, author string) error

// SetUnit включает или выключает узел оборудования вручную.
//
// Подготовка включает всё положенное комнате разом, но по ходу
// занятия дым приходится добавлять, прожекторы гасить, а вытяжку
// пускать раньше срока — поэтому к автоматике добавлено ручное
// управление. В аварии оно недоступно: там состояние выходов задаёт
// аппаратная петля, и спорить с ней из панели нельзя.
func (s *Service) SetUnit(ctx context.Context, id layout.RoomID,
	unit string, on bool, author string) (layout.RoomState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok, err := s.room(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}
	if !ok {
		return layout.RoomState{}, fmt.Errorf("комната %s неизвестна", id)
	}
	if !room.HasUnit(unit) {
		return layout.RoomState{}, fmt.Errorf("%w: в помещении нет узла %q",
			layout.ErrBadTransition, unit)
	}

	current, err := s.State(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}
	if current.State == layout.StateAlarm {
		return layout.RoomState{}, fmt.Errorf(
			"%w: в аварии оборудованием управляет защита", layout.ErrBadTransition)
	}

	if s.units != nil {
		if err := s.units(ctx, room, unit, on, author); err != nil {
			return layout.RoomState{}, err
		}
	}

	// Узел переходит в «ждёт подтверждения»: включённым он станет,
	// когда сборщик ответит, что реле щёлкнуло. До тех пор и панель,
	// и лампа на пульте показывают ожидание, а не результат.
	if unit == layout.UnitSound {
		// Звук исполняет сам центр, сборщика за узлом нет, и ответа
		// ждать не от кого: запись уже пущена или остановлена. Без
		// этого ожидание истекало, узел считался выключенным, и
		// следующее нажатие снова включало звук вместо остановки.
		if current.Equipment == nil {
			current.Equipment = map[string]bool{}
		}
		current.Equipment[unit] = on
		delete(current.Pending, unit)
		delete(current.PendingSince, unit)
	} else {
		if current.Pending == nil {
			current.Pending = map[string]bool{}
		}
		current.Pending[unit] = on
	}
	if unit != layout.UnitSound {
		if current.PendingSince == nil {
			current.PendingSince = map[string]time.Time{}
		}
		current.PendingSince[unit] = time.Now()
	}

	// Отметка времени нужна автоматике: от неё отсчитываются выдержка
	// и период переключения.
	if current.UnitSince == nil {
		current.UnitSince = map[string]time.Time{}
	}
	current.UnitSince[unit] = time.Now()

	// Нажатие человека снимает автоматику по этому узлу до конца
	// цикла комнаты: оператор должен получить то, что нажал.
	if !Auto(author) {
		if current.Manual == nil {
			current.Manual = map[string]bool{}
		}
		current.Manual[unit] = true
	}

	if err := s.states.Save(ctx, current); err != nil {
		return layout.RoomState{}, err
	}

	s.log.Info("узел оборудования переключён",
		"room", id, "узел", unit, "включён", on, "кто", author)
	return current, nil
}

// Confirm закрывает ожидание по ответу сборщика.
//
// Вызывается на каждое подтверждение команды. Принятая команда
// переносит узел в подтверждённое состояние, отклонённая — просто
// снимает ожидание: раз реле не щёлкнуло, показывать включённым
// нечего, и пульт с панелью останутся при прежней картине.
func (s *Service) Confirm(ctx context.Context, room layout.RoomID,
	unit string, want, accepted bool, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()


	if unit == "" {
		return nil
	}

	current, err := s.State(ctx, room)
	if err != nil {
		return err
	}

	pending, was := current.Pending[unit]
	if was && pending != want {
		// Ответ на прежнюю команду, а ждём уже другую. Реле сейчас в
		// положении want — это правда, — но ожидание относится к
		// новой команде, и снимать его рано.
		if accepted {
			if current.Equipment == nil {
				current.Equipment = map[string]bool{}
			}
			current.Equipment[unit] = want
		}
		return s.states.Save(ctx, current)
	}
	s.log.Info("подтверждение узла", "room", room, "узел", unit,
		"включён", want, "принято", accepted, "ждал", was)
	delete(current.Pending, unit)
	delete(current.PendingSince, unit)
	if accepted {
		if current.Equipment == nil {
			current.Equipment = map[string]bool{}
		}
		current.Equipment[unit] = want
	} else {
		s.log.Warn("сборщик отклонил узел",
			"room", room, "узел", unit, "причина", reason)
	}

	return s.states.Save(ctx, current)
}

// ErrNoRoster — подготовку нельзя начать, пока не записан состав.
var ErrNoRoster = errors.New("состав не записан: ФИО и ИИН вносят до подготовки")

// SetRoster записывает состав на предстоящую тренировку.
//
// Состав вносят до подготовки и правят, пока комната не занята: после
// начала занятия он уже принадлежит занятию, а не комнате.
func (s *Service) SetRoster(ctx context.Context, id layout.RoomID,
	team string, rescuers []string, author string) (layout.RoomState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok, err := s.room(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}
	if !ok {
		return layout.RoomState{}, fmt.Errorf("комната %s неизвестна", id)
	}

	current, err := s.State(ctx, id)
	if err != nil {
		return layout.RoomState{}, err
	}
	switch current.State {
	case layout.StateWaiting, layout.StatePreparing, layout.StateReady:
	default:
		return layout.RoomState{}, fmt.Errorf(
			"%w: состав меняют до начала занятия", layout.ErrBadTransition)
	}

	if limit := room.Limit(); limit > 0 && len(rescuers) > limit {
		return layout.RoomState{}, fmt.Errorf(
			"%w: в помещение помещается %d человек", training.ErrCapacity, limit)
	}

	current.Roster = layout.Roster{
		Team:     strings.TrimSpace(team),
		Rescuers: rescuers,
		Author:   author,
		Since:    time.Now(),
	}
	if err := s.states.Save(ctx, current); err != nil {
		return layout.RoomState{}, err
	}

	s.log.Info("состав записан", "room", id,
		"человек", len(rescuers), "звено", current.Roster.Team, "кто", author)
	return current, nil
}

// AlarmMark — отметка, которая ставится занятиям и комнатам при
// аварийной остановке (сценарий §7).
const AlarmMark = "Аварийная остановка"

// Alarms возвращает журнал аварий, новые первыми.
func (s *Service) Alarms(ctx context.Context, limit int) ([]layout.AlarmEvent, error) {
	if s.journal == nil {
		return nil, nil
	}
	return s.journal.Recent(ctx, limit)
}

// newID — идентификатор события журнала.
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// stopSession закрывает занятие с причиной — при аварии результаты и
// видеозаписи сохраняются вместе с отметкой о ней.
func (s *Service) stopSession(ctx context.Context, room layout.RoomID,
	reason string, alarm bool) error {

	if s.finish == nil {
		return fmt.Errorf("закрытие занятия не настроено")
	}
	return s.finish(ctx, room, reason, alarm)
}

// Faults собирает неисправности комнаты для главного экрана.
//
// Экран должен показывать их сам, без похода в журнал: инструктор
// смотрит на комнату, а не на логи сервера.
func (s *Service) faults(room layout.Room, devices []DeviceView,
	heater *DeviceView, cameras []CameraView) []Fault {

	out := make([]Fault, 0)

	for _, d := range devices {
		if d.Device.Kind.Equipment() {
			continue
		}
		if !d.Seen {
			out = append(out, Fault{room.ID, d.Device.ID,
				fmt.Sprintf("%s: не выходил на связь", d.Device.Title())})
			continue
		}
		if d.Stale {
			out = append(out, Fault{room.ID, d.Device.ID,
				fmt.Sprintf("%s: нет связи", d.Device.Title())})
		}
	}

	if heater != nil {
		switch {
		case heater.Seen && heater.Stale:
			out = append(out, Fault{room.ID, heater.Device.ID, "тепловая пушка: нет связи"})
		case heater.Seen && heater.Last.Data.Reason == "sensor_fail":
			out = append(out, Fault{room.ID, heater.Device.ID,
				"нет достоверной температуры: нагрев заблокирован"})
		case heater.Seen && heater.Last.Data.Reason == "overtemp":
			out = append(out, Fault{room.ID, heater.Device.ID,
				"перегрев: нагрев снят защитой"})
		}
	}

	for _, c := range cameras {
		if !c.State.Camera.Enabled {
			continue
		}
		if c.State.Status == video.StatusFailed {
			text := fmt.Sprintf("камера «%s»: нет сигнала", c.State.Camera.Title())
			if c.State.LastError != "" {
				text += " — " + c.State.LastError
			}
			out = append(out, Fault{room.ID, string(c.State.Camera.ID), text})
		}
	}

	return out
}

// resolveAlarm отмечает в журнале, что авария снята.
func (s *Service) resolveAlarm(ctx context.Context) {
	if s.journal == nil {
		return
	}

	events, err := s.journal.Recent(ctx, 1)
	if err != nil || len(events) == 0 || !events[0].Active() {
		return
	}
	// Снимается последняя незакрытая запись: аварии не накладываются
	// друг на друга, комплекс останавливается целиком.
	if err := s.journal.Resolve(ctx, events[0].ID, time.Now()); err != nil {
		s.log.Error("снятие аварии не записано в журнал", "err", err)
	}
}

// Ready проверяет, что комната готова принять команду.
//
// Состояние «Готова» выставляет администратор, осмотрев помещение:
// до этого тренировка не начинается (сценарий §2–3).
func (s *Service) Ready(ctx context.Context, room layout.RoomID) error {
	st, err := s.State(ctx, room)
	if err != nil {
		return err
	}
	if st.State != layout.StateReady {
		// Название помещения обязательно: у дымокамеры занятие одно
		// на две комнаты, и без имени непонятно, какая из них
		// задерживает — дежурный ищет причину наугад.
		name := string(room)
		if r, ok, err := s.room(ctx, room); err == nil && ok {
			name = r.Title()
		}
		return fmt.Errorf("%w: «%s» в состоянии «%s», тренировка начинается из «Готова»",
			layout.ErrBadTransition, name, st.State.Title())
	}
	return nil
}

// Code возвращает код задания, сформированный при подготовке.
func (s *Service) Code(ctx context.Context, room layout.RoomID) (string, error) {
	st, err := s.State(ctx, room)
	if err != nil {
		return "", err
	}
	return st.Code, nil
}

// Began переводит комнату в «Тренировку».
func (s *Service) Began(ctx context.Context, room layout.RoomID, author string) error {
	_, err := s.move(ctx, room, layout.StateTraining, "", author)
	return err
}

// Ended переводит комнату в «Завершение».
func (s *Service) Ended(ctx context.Context, room layout.RoomID, author string) error {
	st, err := s.State(ctx, room)
	if err != nil {
		return err
	}
	// Авария уже перевела комнату: затирать её «Завершением» нельзя.
	if st.Alarm() {
		return nil
	}
	_, err = s.move(ctx, room, layout.StateFinishing, "", author)
	return err
}

// codePanel находит панель набора кода в комнате.
func codePanel(room layout.Room) (string, bool) {
	for _, d := range room.Devices {
		if d.Kind == layout.KindStand {
			return d.ID, true
		}
	}
	return "", false
}

// fires перечисляет имитаторы очага в комнате.
func fires(room layout.Room) []string {
	var out []string
	for _, d := range room.Devices {
		if d.Kind == layout.KindFire {
			out = append(out, d.ID)
		}
	}
	return out
}

// newCode формирует четырёхзначный код.
//
// Источник — crypto/rand: код сообщают вслух и вводят на виду, но
// предсказуемая последовательность позволила бы подобрать его заранее.
func newCode() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Источник случайности на Linux не отказывает; если это
		// всё же случилось, лучше явный код, чем пустой.
		return "0000"
	}
	n := (int(b[0])<<8 | int(b[1])) % 10000
	return fmt.Sprintf("%04d", n)
}

// Rubble отпускает трубы механизма «Завалы» (сценарий дымокамеры §5).
//
// Делается по команде администратора во время тренировки: механизм
// заряжен заранее, а отпускают его, когда команда подошла.
func (s *Service) Rubble(ctx context.Context, id layout.RoomID, author string) error {
	room, ok, err := s.room(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("комната %s неизвестна", id)
	}
	if s.actions == nil {
		return fmt.Errorf("управление оборудованием не настроено")
	}

	st, err := s.State(ctx, id)
	if err != nil {
		return err
	}
	// Вне тренировки трубы не отпускают: под ними может идти осмотр.
	if st.State != layout.StateTraining {
		return fmt.Errorf("%w: завалы включают во время тренировки, комната в состоянии «%s»",
			layout.ErrBadTransition, st.State.Title())
	}

	if err := s.actions.Rubble(ctx, room, author); err != nil {
		return err
	}

	s.log.Info("завалы включены", "room", id, "кто", author)
	return nil
}

// Announce воспроизводит сообщение в комнате.
func (s *Service) Announce(ctx context.Context, id layout.RoomID, sound, author string) error {
	if s.actions == nil {
		return nil
	}
	room, ok, err := s.room(ctx, id)
	if err != nil || !ok {
		return err
	}
	return s.actions.Announce(ctx, room, sound, author)
}

// scenario — состояние узлов помещения по сценарию.
//
// alarm выбирает, чей сценарий брать: старта или аварийной остановки.
// Узлы с alarm: keep сохраняют прежнее состояние — так заданы магниты
// «Завалов».
func scenario(room layout.Room, alarm bool) map[string]bool {
	units := room.Units("")
	out := make(map[string]bool, len(units))
	for _, u := range units {
		if alarm {
			out[u.ID] = u.AlarmOn()
			continue
		}
		out[u.ID] = u.Prepare
	}
	return out
}

// unitSince отмечает все узлы комнаты одним моментом. Вызывается
// там, где сценарий переводит их разом: с этого мгновения автоматика
// и отсчитывает выдержки.
func unitSince(room layout.Room, at time.Time) map[string]time.Time {
	out := make(map[string]time.Time)
	for _, u := range room.Units("") {
		out[u.ID] = at
	}
	return out
}

// alarmPending — узлы, которым при аварии уходит команда. Узлы с
// alarm: keep пропускаются: их цепь независима, команды им нет, и
// ждать по ним подтверждения не от чего.
func alarmPending(room layout.Room) map[string]bool {
	out := make(map[string]bool)
	for _, u := range room.Units("") {
		if u.AlarmKeep() {
			continue
		}
		out[u.ID] = u.AlarmOn()
	}
	return out
}
