// Package training — сценарий занятия: открыть под конкретного
// экзаменуемого, закрыть и посчитать результат.
//
// Счётчики на тренажёрах накопительные и между заездами не
// обнуляются, поэтому результат считается как разница показаний на
// момент открытия и закрытия занятия.
package training

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/rescuer"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/video"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// RecentLimit — сколько прошлых занятий показывать в комнате.
const RecentLimit = 10

// Cards — то, что занятию нужно от картотеки: карточки назначенных
// участников. Интерфейс объявлен здесь, чтобы занятие не зависело от
// сценариев картотеки напрямую.
type Cards interface {
	Resolve(ctx context.Context, iins []rescuer.IIN) ([]rescuer.Card, error)
}

// Rooms — то, что занятию нужно от состояния комнаты.
//
// Тренировка начинается только в подготовленной комнате: администратор
// осмотрел её и подтвердил готовность (сценарий §2–3). Интерфейс
// объявлен здесь, чтобы занятия не зависели от комнат напрямую.
type Rooms interface {
	// Ready проверяет, что комната готова принять команду.
	Ready(ctx context.Context, room layout.RoomID) error
	// Code возвращает код задания, сформированный при подготовке.
	Code(ctx context.Context, room layout.RoomID) (string, error)
	// Roster возвращает состав, записанный до подготовки: занятие
	// открывается по нему, без повторного ввода ФИО перед заходом.
	Roster(ctx context.Context, room layout.RoomID) (layout.Roster, error)
	// Began переводит комнату в «Тренировку».
	Began(ctx context.Context, room layout.RoomID, author string) error
	// Ended переводит комнату в «Завершение».
	Ended(ctx context.Context, room layout.RoomID, author string) error
	// Announce воспроизводит сообщение в комнате.
	Announce(ctx context.Context, room layout.RoomID, sound, author string) error
}

type Service struct {
	layout   port.LayoutRepository
	sessions port.SessionRepository
	state    port.StateRepository
	cards    Cards
	recorder port.Recorder
	history  port.HistoryRepository
	rooms    Rooms
	reports  port.Reports
	log      *slog.Logger
}

func New(l port.LayoutRepository, sessions port.SessionRepository,
	state port.StateRepository, cards Cards, recorder port.Recorder,
	history port.HistoryRepository, log *slog.Logger) *Service {

	return &Service{
		layout: l, sessions: sessions, state: state, cards: cards,
		recorder: recorder, history: history, log: log,
	}
}

// WithRooms подключает состояния комнат. Отдельно от конструктора:
// комнаты и занятия знают друг о друге, и собрать их одним вызовом
// нельзя — кто-то должен быть создан первым.
func (s *Service) WithRooms(rooms Rooms) { s.rooms = rooms }

// WithReports подключает формирование отчётов. Без него занятие
// закрывается как раньше: отчёт — приложение к зачёту, а не условие
// его сохранения.
func (s *Service) WithReports(r port.Reports) { s.reports = r }

// Start открывает занятие в комнате.
//
// operator — имя из сессии оператора, а не из тела запроса: журнал
// занятий бессмыслен, если в нём то, что клиент о себе написал.
// Assignment — назначение спасателя на тренажёр при открытии занятия.
type Assignment struct {
	Device string
	IIN    string
}

// StartOptions — с чем открывается занятие.
type StartOptions struct {
	// Team — название команды: смена, звено, учебная группа.
	Team string
	// Examinee — ФИО, когда занятие открывают без картотеки.
	Examinee string
	// Rescuers — ИИН карточек состава команды.
	Rescuers []string
	// Assignments — кто на каком тренажёре (только теплокамера).
	Assignments []Assignment
}

func (s *Service) Start(ctx context.Context, id layout.RoomID,
	opts StartOptions, operator string) (training.Session, error) {

	raw, rescuers, assignments := opts.Examinee, opts.Rescuers, opts.Assignments
	team := opts.Team

	// Состав записан до подготовки — его и берём, если в запросе
	// ничего не передали: вводить тех же людей второй раз перед
	// заходом не нужно.
	if len(rescuers) == 0 && s.rooms != nil {
		if roster, err := s.rooms.Roster(ctx, id); err == nil && roster.Set() {
			rescuers = roster.Rescuers
			if team == "" {
				team = roster.Team
			}
		}
	}

	room := layout.Room{}

	// Участники назначаются карточками: их показания закрепляются за
	// ИИН, и в карточке занятие появится в истории. Без карточек
	// занятие открывается по одному ФИО — так было до картотеки.
	ids, name, err := s.participants(ctx, rescuers, raw)
	if err != nil {
		return training.Session{}, err
	}

	examinee := training.NormalizeExaminee(name)
	// ФИО проверяется только когда его набрали руками. При назначении
	// карточек имя выведено из картотеки — проверять там нечего, а при
	// звене оно и вовсе перечень через запятую.
	if len(ids) == 0 {
		if err := examinee.Validate(); err != nil {
			return training.Session{}, err
		}
	}

	section, rooms, err2 := s.scope(ctx, id)
	if err2 != nil {
		return training.Session{}, err2
	}
	if len(rooms) == 0 {
		return training.Session{}, fmt.Errorf("комната %s неизвестна", id)
	}
	room = rooms[0]

	// Вместимость помещения: сценарий говорит о 2–3 спасателях
	// одновременно, и сверх неё людей в комнату не заводят.
	if len(ids) > room.Limit() {
		return training.Session{}, fmt.Errorf("%w: в комнату «%s» помещается %d, назначено %d",
			training.ErrCapacity, room.Title(), room.Limit(), len(ids))
	}

	// Тренировка начинается только в готовой комнате: её осмотрел и
	// подтвердил администратор (сценарий §2–3). Готовы должны быть все
	// комнаты секции: маршрут идёт через них подряд.
	if s.rooms != nil {
		for _, r := range rooms {
			if err := s.rooms.Ready(ctx, r.ID); err != nil {
				return training.Session{}, err
			}
		}
	}

	// Второе занятие в той же комнате означало бы, что показания
	// одних тренажёров зачтутся двоим.
	if cur, ok, err := s.sessions.Active(ctx, string(section.Primary())); err != nil {
		return training.Session{}, err
	} else if ok {
		return training.Session{}, fmt.Errorf("%w: %s, начато в %s",
			training.ErrBusy, cur.Examinee, cur.Started.Format("15:04"))
	}

	counters, err := s.counters(ctx, id)
	if err != nil {
		return training.Session{}, err
	}

	sess := training.Session{
		ID: training.SessionID(newID()),
		// Занятие записывается на первую комнату секции: по ней его
		// ищут, а список всех комнат маршрута лежит рядом.
		Room:     string(section.Primary()),
		Section:  section.ID,
		Rooms:    roomIDs(rooms),
		Examinee: examinee,
		Team:     strings.TrimSpace(team),
		Rescuers: ids,
		Operator: operator,
		Started:  time.Now(),
		AtStart:  counters,
	}
	// Код задания «Набор чисел» сформирован при подготовке:
	// администратор уже сообщил его участникам.
	if s.rooms != nil {
		for _, r := range rooms {
			if code, err := s.rooms.Code(ctx, r.ID); err == nil && code != "" {
				sess.Code = code
				break
			}
		}
	}

	// Назначения на тренажёры: показания каждого датчика закрепляются
	// за своей карточкой (сценарий теплокамеры §4).
	for _, a := range assignments {
		if err := s.assign(ctx, room, &sess, a.Device, a.IIN); err != nil {
			return training.Session{}, err
		}
	}

	if err := s.sessions.Save(ctx, sess); err != nil {
		return training.Session{}, err
	}

	// Видеозапись начинается вместе с таймером и учётом показаний
	// (сценарий §4). Пишутся камеры всех комнат маршрута. Ошибка
	// записи занятие не отменяет: тренировка важнее ролика.
	if s.recorder != nil {
		var cameras []video.Camera
		for _, r := range rooms {
			cameras = append(cameras, r.Cameras...)
		}
		s.recorder.Start(ctx, string(sess.ID), cameras)
	}

	// Комнаты маршрута переходят в «Тренировку» вместе с открытием
	// занятия: иначе экран показывал бы готовность при идущем заезде.
	if s.rooms != nil {
		for _, r := range rooms {
			if err := s.rooms.Began(ctx, r.ID, operator); err != nil {
				s.log.Error("комната не переведена в тренировку", "room", r.ID, "err", err)
			}
		}
	}

	s.log.Info("занятие начато", "room", id, "команда", sess.TeamName(),
		"человек", len(ids), "назначений", len(sess.Current()), "operator", operator)
	return sess, nil
}

// Assign ставит спасателя на тренажёр во время занятия.
//
// Прежнее назначение этого тренажёра закрывается: результат
// завершённого упражнения остаётся за прежним участником, а новое
// учитывается отдельно (сценарий §5).
func (s *Service) Assign(ctx context.Context, id layout.RoomID,
	device, iin string) (training.Session, error) {

	room, ok, err := s.room(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, fmt.Errorf("комната %s неизвестна", id)
	}

	sess, ok, err := s.active(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, training.ErrNoSession
	}

	if err := s.assign(ctx, room, &sess, device, iin); err != nil {
		return training.Session{}, err
	}
	if err := s.sessions.Save(ctx, sess); err != nil {
		return training.Session{}, err
	}

	s.log.Info("назначение на тренажёр", "room", id, "device", device, "iin", iin)
	return sess, nil
}

// assign проверяет и записывает назначение в занятие.
//
// Пустой iin снимает спасателя с тренажёра: упражнение закрывается,
// результат остаётся за ним.
func (s *Service) assign(ctx context.Context, room layout.Room,
	sess *training.Session, device, iin string) error {

	dev, ok := room.Device(device)
	if !ok {
		return fmt.Errorf("%w: в комнате «%s» нет устройства %s",
			training.ErrNotAssigned, room.Title(), device)
	}
	// Поимённо назначают только на тренажёры. Задания дымокамеры
	// проходят звеном, и их результат связывается со всеми картами.
	if !dev.Kind.Trainer() {
		return fmt.Errorf("%w: «%s» — не тренажёр, его проходят звеном",
			training.ErrNotAssigned, dev.Title())
	}

	id := string(rescuer.NormalizeIIN(iin))
	if id != "" {
		if !sess.HasRescuer(id) {
			return fmt.Errorf("%w: карточка %s не назначена на это занятие",
				training.ErrNotAssigned, id)
		}
		// Один человек — один тренажёр: иначе его показания считались
		// бы дважды.
		if busy, ok := sess.Occupies(id); ok && busy != device {
			return fmt.Errorf("%w: карточка %s уже на тренажёре %s",
				training.ErrNotAssigned, id, busy)
		}
	}

	now := time.Now()
	counters, err := s.deviceCounters(ctx, dev)
	if err != nil {
		return err
	}

	// Закрываем прежнее назначение этого тренажёра.
	for i := range sess.Assignments {
		a := &sess.Assignments[i]
		if a.Device == device && a.Running() {
			a.Until = now
			a.AtEnd = counters
		}
	}
	if id == "" {
		return nil
	}

	sess.Assignments = append(sess.Assignments, training.Assignment{
		Device:  device,
		IIN:     id,
		Since:   now,
		AtStart: counters,
	})
	return nil
}

// codeAttempts считает попытки ввода кода за занятие.
func (s *Service) codeAttempts(ctx context.Context, sess training.Session) int {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return 0
	}

	for _, id := range sess.Rooms {
		for _, r := range obj.Rooms {
			if r.ID != layout.RoomID(id) {
				continue
			}
			for _, t := range r.Tasks {
				if t.Device == "" {
					continue
				}
				if n := s.attempts(ctx, t, sess.Started); n > 0 {
					return n
				}
			}
		}
	}
	return 0
}

// temperature считает сводку температуры за время занятия.
//
// Берётся из истории телеметрии: отдельно копить показания значило бы
// держать второй источник правды о том же датчике. Без базы истории
// нет — тогда сводка остаётся пустой, и карточка честно об этом молчит.
func (s *Service) temperature(ctx context.Context, room layout.Room,
	from, to time.Time) training.Temp {

	source := room.TempSource()
	if s.history == nil || source == "" {
		return training.Temp{}
	}

	packets, err := s.history.History(ctx, port.HistoryQuery{
		Device: telemetry.DeviceID(source),
		From:   from,
		To:     to,
		Limit:  10000,
	})
	if err != nil {
		s.log.Error("температура за занятие не посчитана", "room", room.ID, "err", err)
		return training.Temp{}
	}

	var out training.Temp
	var sum float64
	for _, p := range packets {
		t := p.Data.TempC
		// Нулевое значение означает, что температуры в пакете нет:
		// события реле и показания других датчиков её не несут.
		if t == 0 {
			continue
		}
		if out.Samples == 0 || t < out.MinC {
			out.MinC = t
		}
		if out.Samples == 0 || t > out.MaxC {
			out.MaxC = t
		}
		sum += t
		out.Samples++
	}
	if out.Samples > 0 {
		out.AvgC = sum / float64(out.Samples)
	}
	return out
}

// Comment записывает комментарий администратора к занятию.
//
// Правится после: разбор бывает не сразу, и первая формулировка
// нередко оказывается неполной.
func (s *Service) Comment(ctx context.Context, id, text, author string) (training.Session, error) {
	sess, ok, err := s.sessions.Get(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, training.ErrNoSession
	}

	sess.Comment = strings.TrimSpace(text)
	if err := s.sessions.Save(ctx, sess); err != nil {
		return training.Session{}, err
	}

	s.log.Info("комментарий к занятию", "session", id, "кто", author)
	return sess, nil
}

// deviceCounters снимает показания одного тренажёра.
func (s *Service) deviceCounters(ctx context.Context, d layout.Device) (training.Counters, error) {
	last, seen, err := s.state.Latest(ctx, telemetry.DeviceID(d.ID))
	if err != nil || !seen {
		return training.Counters{}, err
	}

	var c training.Counters
	switch d.Kind {
	case layout.KindBike:
		c.DistanceM = last.Data.DistanceM
	case layout.KindStairs:
		c.Floors = last.Data.Floors
		c.Steps = last.Data.StepCount
	case layout.KindLoad:
		c.Sets = last.Data.Sets
		c.Reps = last.Data.Reps
		c.MaxLoadKG = last.Data.LoadKG
	}
	return c, nil
}

// Finish закрывает занятие и считает результат.
func (s *Service) Finish(ctx context.Context, id layout.RoomID) (training.Session, error) {
	return s.finish(ctx, id, "")
}

// FinishByAlarm закрывает занятие аварийной остановкой: в карточке
// остаётся отметка «Аварийная остановка», а не обычное досрочное
// завершение (сценарий §7).
func (s *Service) FinishByAlarm(ctx context.Context, id layout.RoomID,
	reason string) (training.Session, error) {

	if reason == "" {
		reason = AlarmMark
	}
	return s.finishWith(ctx, id, reason, training.OutcomeAlarm)
}

// AlarmMark — отметка аварийной остановки в карточке участника.
const AlarmMark = "Аварийная остановка"

// FinishWithReason закрывает занятие досрочно — аварией или решением
// администратора. Результаты и ролики при этом сохраняются.
func (s *Service) FinishWithReason(ctx context.Context, id layout.RoomID,
	reason string) (training.Session, error) {

	return s.finish(ctx, id, reason)
}

// finish закрывает занятие одним сохранением: причина проставляется
// до записи, иначе пришлось бы сохранять дважды.
func (s *Service) finish(ctx context.Context, id layout.RoomID,
	reason string) (training.Session, error) {

	return s.finishWith(ctx, id, reason, "")
}

// finishWith закрывает занятие; outcome задаётся, когда итог известен
// снаружи, — так авария остаётся аварией, сколько бы заданий ни
// успели отметить.
func (s *Service) finishWith(ctx context.Context, id layout.RoomID,
	reason string, outcome training.Outcome) (training.Session, error) {
	sess, ok, err := s.active(ctx, id)
	if err != nil {
		return training.Session{}, err
	}
	if !ok {
		return training.Session{}, training.ErrNoSession
	}

	room, _, err := s.room(ctx, id)
	if err != nil {
		return training.Session{}, err
	}

	counters, err := s.counters(ctx, id)
	if err != nil {
		return training.Session{}, err
	}

	now := time.Now()
	// Назначения закрываются вместе с занятием: иначе упражнение
	// считалось бы идущим после его конца.
	for i := range sess.Assignments {
		a := &sess.Assignments[i]
		if !a.Running() {
			continue
		}
		dev, ok := room.Device(a.Device)
		if !ok {
			a.Until = now
			continue
		}
		end, err := s.deviceCounters(ctx, dev)
		if err != nil {
			return training.Session{}, err
		}
		a.Until = now
		a.AtEnd = end
	}

	// Запись останавливается до сохранения: ролики должны попасть в
	// ту же запись занятия, что и результаты.
	if s.recorder != nil {
		sess.Records = s.recorder.Stop(string(sess.ID))
	}

	// Попытки ввода кода считаются и при завершении: занятие могли
	// закрыть, ни разу не открыв экран заданий.
	if n := s.codeAttempts(ctx, sess); n > sess.Attempts {
		sess.Attempts = n
	}

	sess.Finished = now
	sess.Reason = reason
	if outcome != "" {
		sess.Outcome = outcome
	} else {
		sess.Outcome = s.outcome(ctx, sess, reason)
	}
	sess.AtFinish = counters
	// Температура помещения за время занятия: в карточке смотрят,
	// в каких условиях человек работал (сценарий §6).
	sess.Temp = s.temperature(ctx, room, sess.Started, now)

	// Отчёт складывается до сохранения: он переносит ролики в свою
	// папку, и в карточке должны стоять уже новые пути.
	if s.reports != nil {
		if out, err := s.reports.Write(ctx, sess, s.reportInfo(ctx, sess)); err != nil {
			// Отчёт не удался — занятие всё равно сохраняется: зачёт
			// важнее бумаги, а причину видно в журнале.
			s.log.Error("отчёт о тренировке не сформирован",
				"session", sess.ID, "err", err)
		} else {
			sess = out
		}
	}

	if err := s.sessions.Save(ctx, sess); err != nil {
		return training.Session{}, err
	}

	if reason != "" {
		s.log.Warn("занятие прервано", "room", id, "причина", reason)
	}

	// Комнаты маршрута переходят в «Завершение»: участники выходят,
	// данные сохраняются.
	if s.rooms != nil {
		for _, r := range sess.Rooms {
			if err := s.rooms.Ended(ctx, layout.RoomID(r), ""); err != nil {
				s.log.Error("комната не переведена в завершение", "room", r, "err", err)
			}
		}
	}

	result := sess.Result()
	s.log.Info("занятие завершено",
		"итог", sess.Status(),
		"room", id, "examinee", sess.Examinee,
		"длительность", sess.Duration().Round(time.Second),
		"путь_м", int(result.DistanceM), "этажей", result.Floors, "подходов", result.Sets)

	return sess, nil
}

// Active возвращает открытое занятие комнаты.
//
// Занятие ищется по секции: у дымокамеры оно одно на две комнаты, и
// обе должны показывать его как идущее.
func (s *Service) Active(ctx context.Context, room layout.RoomID) (training.Session, bool, error) {
	return s.active(ctx, room)
}

func (s *Service) active(ctx context.Context, room layout.RoomID) (training.Session, bool, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return training.Session{}, false, err
	}
	return s.sessions.Active(ctx, string(obj.SectionOf(room).Primary()))
}

// All возвращает последние закрытые занятия объекта — журнал
// результатов целиком, без разбивки по комнатам.
func (s *Service) All(ctx context.Context, limit int) ([]training.Session, error) {
	if limit <= 0 {
		limit = RecentLimit
	}
	return s.sessions.Recent(ctx, "", limit)
}

// Recent возвращает последние закрытые занятия комнаты.
//
// Занятия ищутся по секции: у дымокамеры одно занятие на две комнаты,
// и страница второй комнаты должна показывать тот же итог, а не
// пустой журнал.
func (s *Service) Recent(ctx context.Context, room layout.RoomID) ([]training.Session, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return nil, err
	}
	return s.sessions.Recent(ctx, string(obj.SectionOf(room).Primary()), RecentLimit)
}

// participants проверяет назначенные карточки и собирает имя занятия.
//
// Имя берётся из переданного ФИО, а при назначении карточек — из них
// самих: в журнале должно стоять то, что в картотеке, а не то, что
// набрали в поле.
func (s *Service) participants(ctx context.Context, rescuers []string,
	raw string) ([]string, string, error) {

	ids := make([]string, 0, len(rescuers))
	for _, r := range rescuers {
		id := rescuer.NormalizeIIN(r)
		if id == "" {
			continue
		}
		ids = append(ids, string(id))
	}
	if len(ids) == 0 {
		return nil, raw, nil
	}

	if s.cards == nil {
		return nil, "", fmt.Errorf("картотека не подключена: назначить карточки нельзя")
	}

	typed := make([]rescuer.IIN, 0, len(ids))
	for _, id := range ids {
		typed = append(typed, rescuer.IIN(id))
	}

	cards, err := s.cards.Resolve(ctx, typed)
	if err != nil {
		return nil, "", err
	}

	// Имя занятия — из карточек, а не из поля ввода: в журнале должно
	// стоять то, что в картотеке. При звене это перечень участников.
	names := make([]string, 0, len(cards))
	for _, c := range cards {
		names = append(names, string(c.Name))
	}
	return ids, strings.Join(names, ", "), nil
}

// counters снимает показания всех тренажёров комнаты.
func (s *Service) counters(ctx context.Context, id layout.RoomID) (training.Counters, error) {
	room, ok, err := s.room(ctx, id)
	if err != nil || !ok {
		return training.Counters{}, err
	}

	var c training.Counters
	for _, d := range room.Devices {
		last, seen, err := s.state.Latest(ctx, telemetry.DeviceID(d.ID))
		if err != nil {
			return training.Counters{}, err
		}
		if !seen {
			continue
		}

		switch d.Kind {
		case layout.KindBike:
			c.DistanceM = last.Data.DistanceM
		case layout.KindStairs:
			c.Floors = last.Data.Floors
			c.Steps = last.Data.StepCount
		case layout.KindLoad:
			c.Sets = last.Data.Sets
			c.Reps = last.Data.Reps
			c.MaxLoadKG = max(c.MaxLoadKG, last.Data.LoadKG)
		}
	}
	return c, nil
}

// scope возвращает секцию комнаты и её комнаты по порядку маршрута.
//
// Тренировка идёт по секции: у дымокамеры это две комнаты подряд с
// общим таймером, записью и зачётом.
func (s *Service) scope(ctx context.Context, id layout.RoomID) (layout.Section, []layout.Room, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return layout.Section{}, nil, err
	}

	var known bool
	for _, r := range obj.Rooms {
		if r.ID == id {
			known = true
			break
		}
	}
	if !known {
		return layout.Section{}, nil, nil
	}

	section := obj.SectionOf(id)
	return section, obj.RoomsOf(section), nil
}

func roomIDs(rooms []layout.Room) []string {
	out := make([]string, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, string(r.ID))
	}
	return out
}

func (s *Service) room(ctx context.Context, id layout.RoomID) (layout.Room, bool, error) {
	obj, err := s.layout.Object(ctx)
	if err != nil {
		return layout.Room{}, false, err
	}
	for _, r := range obj.Rooms {
		if r.ID == id {
			return r, true, nil
		}
	}
	return layout.Room{}, false, nil
}

func newID() string {
	var b [8]byte
	// Источник случайности не может отказать: crypto/rand на Linux
	// читает из ядра, и ошибка здесь означала бы, что системе конец.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
