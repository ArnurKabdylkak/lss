package training

import (
	"context"
	"fmt"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Задания маршрута: что команда уже прошла.
//
// Задание проходит команда целиком, поэтому отметка одна и
// связывается с карточками всех участников. Часть заданий
// отмечается сама — по сигналу устройства, часть ставит
// администратор, наблюдая за выполнением (сценарий дымокамеры §4–5).

// Task — задание вместе с отметкой о прохождении.
type Task struct {
	Task layout.Task
	// Room — помещение, которому задание принадлежит. У дымокамеры
	// маршрут идёт через две комнаты, и в окне каждой показывают её
	// часть, а не весь список.
	Room layout.RoomID
	Done bool
	At   time.Time
	By   string
	Auto bool
}

// Tasks возвращает задания комнаты с отметками.
//
// Автоматические отмечаются здесь же: сигнал приходит телеметрией, и
// отдельный опрос завёл бы второй источник правды о том же событии.
func (s *Service) Tasks(ctx context.Context, id layout.RoomID) ([]Task, error) {
	_, rooms, err := s.scope(ctx, id)
	if err != nil {
		return nil, err
	}

	// Задания всех комнат маршрута в одном списке: занятие у секции
	// одно, и отметки считаются по нему целиком. Кому какое задание
	// принадлежит, помнит поле Room.
	type owned struct {
		task layout.Task
		room layout.RoomID
	}
	var tasks []owned
	for _, r := range rooms {
		for _, t := range r.Tasks {
			tasks = append(tasks, owned{task: t, room: r.ID})
		}
	}
	if len(tasks) == 0 {
		return nil, nil
	}

	sess, active, err := s.active(ctx, id)
	if err != nil {
		return nil, err
	}

	var changed bool
	out := make([]Task, 0, len(tasks))

	for _, o := range tasks {
		t := o.task
		item := Task{Task: t, Room: o.room}
		if !active {
			out = append(out, item)
			continue
		}

		mark := sess.Tasks[t.ID]
		if !mark.Done && t.Auto() {
			if n := s.attempts(ctx, t, sess.Started); n > sess.Attempts {
				// Неверный ввод задание не завершает, но учитывается:
				// по числу попыток видно, как шло выполнение.
				sess.Attempts = n
				changed = true
			}
			if done, at := s.signalled(ctx, t, sess.Started); done {
				mark = training.TaskMark{Done: true, At: at, Auto: true}
				if sess.Tasks == nil {
					sess.Tasks = make(map[string]training.TaskMark, len(tasks))
				}
				sess.Tasks[t.ID] = mark
				changed = true

				s.log.Info("задание засчитано по сигналу",
					"room", id, "task", t.ID, "device", t.Device)
			}
		}

		item.Done, item.At, item.By, item.Auto = mark.Done, mark.At, mark.By, mark.Auto
		out = append(out, item)
	}

	if changed {
		if err := s.sessions.Save(ctx, sess); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// attempts считает попытки ввода кода за занятие.
//
// Берётся из истории телеметрии: событий ввода может быть много, и
// держать их счётчик отдельно значило бы заводить второй источник
// правды о том же. Без базы истории нет — тогда попытки не считаются.
func (s *Service) attempts(ctx context.Context, t layout.Task, since time.Time) int {
	if s.history == nil || t.Device == "" {
		return 0
	}

	packets, err := s.history.History(ctx, port.HistoryQuery{
		Device:     telemetry.DeviceID(t.Device),
		From:       since,
		To:         time.Now(),
		EventsOnly: true,
		Limit:      1000,
	})
	if err != nil {
		return 0
	}

	var n int
	for _, p := range packets {
		if !p.Data.IsCodeEvent() {
			continue
		}
		// Попытка — это набранный и подтверждённый код. Стирание
		// набора и пауза после подбора попытками не являются: иначе
		// человек, сбросивший случайное нажатие, выглядел бы в
		// отчёте как подбиравший код.
		switch p.Data.CodeResult {
		case "valid", "invalid":
			n++
		}
	}
	return n
}

// signalled проверяет, пришёл ли сигнал о выполнении задания.
//
// ЧЕРНОВИК: контракт этих устройств не согласован. Пока засчитываются
// событие верного кода со стойки и событие отключения имитатора очага
// — то, что уже есть в протоколе.
func (s *Service) signalled(ctx context.Context, t layout.Task,
	since time.Time) (bool, time.Time) {

	// Смотрим последнее событие, а не последний пакет: за событием
	// плата шлёт обычный отчёт о себе, и тот затирал бы тушение
	// очага или верный код раньше, чем панель успеет спросить.
	latest := s.state.Latest
	if ev, ok := s.state.(eventSource); ok {
		latest = ev.LatestEvent
	}
	last, seen, err := latest(ctx, telemetry.DeviceID(t.Device))
	if err != nil || !seen {
		return false, time.Time{}
	}
	// Сигнал до начала занятия не в счёт: иначе задание зачлось бы
	// прошлой командой.
	if last.Received.Before(since) {
		return false, time.Time{}
	}

	switch {
	case last.Data.IsCodeEvent() && last.Data.CodeResult == "valid":
		return true, last.Received
	case last.Data.Event == telemetry.EventRelay && last.Data.State == "off":
		// Имитатор очага отключён кнопкой.
		return true, last.Received
	default:
		return false, time.Time{}
	}
}

// MarkTask ставит или снимает отметку о прохождении задания вручную.
func (s *Service) MarkTask(ctx context.Context, id layout.RoomID,
	task string, done bool, author string) (training.Session, error) {

	_, rooms, err := s.scope(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if len(rooms) == 0 {
		return training.Session{}, fmt.Errorf("комната %s неизвестна", id)
	}

	var t layout.Task
	var found bool
	for _, r := range rooms {
		if item, ok := r.Task(task); ok {
			t, found = item, true
			break
		}
	}
	if !found {
		return training.Session{}, fmt.Errorf("%w: на маршруте нет задания %s",
			training.ErrNotAssigned, task)
	}

	sess, ok, err := s.active(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, training.ErrNoSession
	}

	if sess.Tasks == nil {
		sess.Tasks = make(map[string]training.TaskMark, len(rooms))
	}
	if done {
		sess.Tasks[t.ID] = training.TaskMark{Done: true, At: time.Now(), By: author}
	} else {
		// Снятие отметки нужно, когда её поставили по ошибке: журнал
		// занятия важнее удобства, и переписывать его нельзя, а вот
		// идущее занятие поправить можно.
		delete(sess.Tasks, t.ID)
	}

	if err := s.sessions.Save(ctx, sess); err != nil {
		return training.Session{}, err
	}

	s.log.Info("отметка о задании", "room", id, "task", t.ID,
		"пройдено", done, "кто", author)

	// Весь маршрут пройден — тренировка завершается сама и звучит
	// сообщение об успехе (сценарий дымокамеры §5). Ждать, пока
	// администратор нажмёт «Завершить», незачем: команда уже вышла.
	if done && s.allDone(ctx, rooms, sess) {
		if s.rooms != nil {
			if err := s.rooms.Announce(ctx, id, telemetry.SoundSuccess, author); err != nil {
				s.log.Error("сообщение об успехе не воспроизведено", "room", id, "err", err)
			}
		}

		finished, err := s.finish(ctx, id, "")
		if err != nil {
			return training.Session{}, err
		}
		s.log.Info("маршрут пройден полностью, тренировка завершена",
			"room", id, "команда", finished.TeamName())
		return finished, nil
	}

	return sess, nil
}

// allDone сообщает, что пройдены все задания маршрута.
func (s *Service) allDone(_ context.Context, rooms []layout.Room, sess training.Session) bool {
	var total int
	for _, r := range rooms {
		for _, t := range r.Tasks {
			total++
			if !sess.TaskDone(t.ID) {
				return false
			}
		}
	}
	return total > 0
}

// outcome выставляет итог занятия при его закрытии.
//
// Прерванное занятие остаётся прерванным, даже если задания успели
// отметить: по сценарию §7 авария закрывает тренировку независимо от
// того, сколько уже пройдено.
func (s *Service) outcome(ctx context.Context, sess training.Session,
	reason string) training.Outcome {
	if reason != "" {
		return training.OutcomeAborted
	}

	rooms, err := s.routeRooms(ctx, sess)
	if err != nil {
		s.log.Error("итог занятия не определён", "session", sess.ID, "err", err)
		return training.OutcomeDone
	}

	// Маршрут есть — зачёт по нему: дымокамера.
	if routeTasks(rooms) > 0 {
		if s.allDone(ctx, rooms, sess) {
			return training.OutcomeSuccess
		}
		return training.OutcomePartial
	}

	// Маршрута нет — теплокамера: зачёт по заданиям тренажёрам.
	// Раньше итог считался только по маршруту, и занятие, закрытое
	// кнопкой, называлось «Не завершена», хотя завершать было нечего.
	if set, met := targetsMet(sess); set {
		if met {
			return training.OutcomeSuccess
		}
		return training.OutcomePartial
	}

	// Ни маршрута, ни заданий: занятие просто проведено.
	return training.OutcomeDone
}

// routeTasks считает задания маршрута помещений.
func routeTasks(rooms []layout.Room) int {
	var n int
	for _, r := range rooms {
		n += len(r.Tasks)
	}
	return n
}

// targetsMet сообщает, были ли заданы упражнения и выполнены ли они.
//
// Задание относится к тренажёру: кто на нём работал и сколько раз
// менялся, значения не имеет — считается сумма по всем назначениям.
func targetsMet(sess training.Session) (set, met bool) {
	if len(sess.Targets) == 0 {
		return false, false
	}

	met = true
	for device, target := range sess.Targets {
		if !target.Set() {
			continue
		}
		set = true

		var elapsed time.Duration
		var counters training.Counters
		for _, a := range sess.Assignments {
			if a.Device != device {
				continue
			}
			elapsed += a.Duration()
			r := a.Result()
			counters.Sets += r.Sets
			counters.DistanceM += r.DistanceM
			counters.Floors += r.Floors
			counters.Steps += r.Steps
			if r.MaxLoadKG > counters.MaxLoadKG {
				counters.MaxLoadKG = r.MaxLoadKG
			}
		}

		if !target.Done(counters, elapsed) {
			met = false
		}
	}
	return set, met
}

// routeRooms возвращает комнаты маршрута занятия.
//
// Берутся комнаты, записанные в самом занятии: раскладку могли
// изменить, а зачёт выставляется по тому маршруту, который команда
// проходила.
func (s *Service) routeRooms(ctx context.Context, sess training.Session) ([]layout.Room, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return nil, err
	}

	ids := sess.Rooms
	if len(ids) == 0 {
		ids = []string{sess.Room}
	}

	rooms := make([]layout.Room, 0, len(ids))
	for _, id := range ids {
		for _, room := range obj.Rooms {
			if room.ID == layout.RoomID(id) {
				rooms = append(rooms, room)
				break
			}
		}
	}
	return rooms, nil
}

// reportInfo собирает для отчёта то, чего нет в самом занятии:
// названия комнат и заданий, имена и подразделения участников.
func (s *Service) reportInfo(ctx context.Context, sess training.Session) port.ReportInfo {
	info := port.ReportInfo{
		RoomNames:   map[string]string{},
		DeviceNames: map[string]string{},
		TaskNames:   map[string]string{},
		Names:       map[string]string{},
		Units:       map[string]string{},
	}

	if rooms, err := s.routeRooms(ctx, sess); err == nil {
		for _, r := range rooms {
			info.RoomNames[string(r.ID)] = r.Title()
			for _, d := range r.Devices {
				info.DeviceNames[d.ID] = d.Name
			}
			for _, t := range r.Tasks {
				info.TaskNames[t.ID] = t.Name
			}
		}
	}

	if s.cards != nil && len(sess.Rescuers) > 0 {
		iins := make([]rescuer.IIN, 0, len(sess.Rescuers))
		for _, iin := range sess.Rescuers {
			iins = append(iins, rescuer.IIN(iin))
		}
		// Карточка могла уйти в архив после занятия — имя всё равно
		// нужно: отчёт описывает тот день.
		if cards, err := s.cards.Resolve(ctx, iins); err == nil {
			for _, c := range cards {
				info.Names[string(c.IIN)] = string(c.Name)
				info.Units[string(c.IIN)] = c.Unit
			}
		}
	}
	return info
}

// eventSource — хранилище, которое помнит события устройств отдельно
// от их обычных отчётов.
type eventSource interface {
	LatestEvent(ctx context.Context, id telemetry.DeviceID) (telemetry.Packet, bool, error)
}
