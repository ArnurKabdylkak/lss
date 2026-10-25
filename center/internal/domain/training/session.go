// Package training — занятие: кто сейчас на тренажёрах комнаты и с
// каким результатом он закончил.
//
// Без имени экзаменуемого показания — это поток чисел, который нельзя
// приобщить к зачёту: через час никто не вспомнит, чей был заезд.
// Поэтому занятие открывается до начала и закрывается после.
package training

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

var (
	// ErrNoExaminee — занятие без имени экзаменуемого не открывается.
	ErrNoExaminee = errors.New("не указано ФИО экзаменуемого")

	// ErrBadExaminee — имя не похоже на ФИО.
	ErrBadExaminee = errors.New("ФИО записано неверно")

	// ErrBusy — в комнате уже идёт занятие.
	ErrBusy = errors.New("в комнате уже идёт занятие")

	// ErrNoSession — занятия в комнате нет.
	ErrNoSession = errors.New("в комнате нет открытого занятия")

	// ErrCapacity — в комнату назначено больше людей, чем помещается.
	ErrCapacity = errors.New("превышена вместимость помещения")

	// ErrNotAssigned — назначение невозможно: такого тренажёра в
	// комнате нет, он занят или спасатель не в составе занятия.
	ErrNotAssigned = errors.New("назначение невозможно")
)

// MaxExamineeLen — предел длины ФИО. Длиннее — это уже не имя,
// а чья-то попытка положить в поле что-то другое.
const MaxExamineeLen = 120

// Examinee — ФИО экзаменуемого.
type Examinee string

// NormalizeExaminee приводит ввод к виду, пригодному для зачёта:
// убирает лишние пробелы по краям и внутри.
//
// Оператор набирает ФИО на ходу, и двойные пробелы в списке потом
// выглядят как разные люди.
func NormalizeExaminee(s string) Examinee {
	return Examinee(strings.Join(strings.Fields(s), " "))
}

// Validate проверяет ФИО.
//
// Проверка намеренно мягкая: фамилии бывают с дефисом и апострофом,
// отчества может не быть вовсе. Отсекается пустое, слишком длинное и
// то, в чём нет ни одной буквы, — остальное дело оператора.
func (e Examinee) Validate() error {
	s := string(e)
	if s == "" {
		return ErrNoExaminee
	}
	if len([]rune(s)) > MaxExamineeLen {
		return fmt.Errorf("%w: длиннее %d символов", ErrBadExaminee, MaxExamineeLen)
	}

	var letters int
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsSpace(r), r == '-', r == '\'', r == '.':
		default:
			return fmt.Errorf("%w: недопустимый символ %q", ErrBadExaminee, r)
		}
	}
	if letters < 2 {
		return fmt.Errorf("%w: в имени нет букв", ErrBadExaminee)
	}
	return nil
}

// Counters — накопительные показания тренажёров комнаты на момент
// времени. По разнице начала и конца считается результат занятия.
type Counters struct {
	DistanceM float64 // велосипед, метры
	Floors    uint16  // лестница, пройдено этажей
	Steps     uint16  // лестница, ступеней
	Sets      uint16  // силовая установка, подходов
	Reps      uint16  // силовая установка, подъёмов за занятие (накопительно)
	MaxLoadKG float64 // силовая установка, наибольший вес
}

// Sub возвращает прирост относительно начала занятия.
//
// Счётчики на платах монотонны, но плата может перезапуститься —
// тогда конечное значение окажется меньше начального. Отрицательный
// результат бессмысленнее нуля, поэтому он обрезается.
func (c Counters) Sub(start Counters) Counters {
	return Counters{
		DistanceM: max(c.DistanceM-start.DistanceM, 0),
		Floors:    subU16(c.Floors, start.Floors),
		Steps:     subU16(c.Steps, start.Steps),
		Sets:      subU16(c.Sets, start.Sets),
		// Подъёмы — накопительный счётчик платы, как и остальные:
		// результат занятия считается приростом.
		Reps:      subU16(c.Reps, start.Reps),
		MaxLoadKG: max(c.MaxLoadKG, start.MaxLoadKG),
	}
}

func subU16(a, b uint16) uint16 {
	if a < b {
		return 0
	}
	return a - b
}

// SessionID — идентификатор занятия.
type SessionID string

// Session — занятие в комнате.
type Session struct {
	ID   SessionID
	Room string

	// Section — секция, на которую открыто занятие, и её комнаты.
	// У дымокамеры маршрут идёт через две комнаты подряд, а таймер,
	// запись и зачёт у команды общие.
	Section  string
	Rooms    []string
	Examinee Examinee
	Operator string // кто открыл занятие, из сессии оператора

	// Team — название команды, проходящей тренировку. Тренируется
	// команда целиком, а карточки входят в её состав: результат и
	// видеозапись связываются с карточкой каждого участника.
	Team string

	// Rescuers — ИИН карточек состава команды. Пусто — занятие
	// открыто без картотеки, по одному ФИО.
	Rescuers []string

	// Assignments — кто какой тренажёр занимает и занимал. Закрытые
	// записи не удаляются: по ним считается время и результат каждого
	// упражнения.
	Assignments []Assignment

	// Records — видеозаписи занятия, по одной на камеру. Ролик
	// охватывает только время занятия: подготовка в него не входит
	// (сценарий §6).
	Records []Record

	// Targets — задания по тренажёрам: устройство -> цель.
	Targets map[string]Target

	// Tasks — отметки о прохождении заданий маршрута: задание ->
	// отметка. Задание проходит команда целиком.
	Tasks map[string]TaskMark

	// Code — код задания «Набор чисел» на эту тренировку, Attempts —
	// сколько раз его вводили. Неверный ввод задание не завершает,
	// но учитывается (сценарий дымокамеры §4).
	Code     string
	Attempts int

	// Temp — температура помещения за время занятия (сценарий §6).
	Temp Temp

	// Comment — комментарий администратора. Пишется после занятия
	// и правится: разбор бывает не сразу.
	Comment string

	Started  time.Time
	Finished time.Time // нулевое — занятие идёт

	// Report — папка отчёта внутри хранилища записей: в ней лежат
	// PDF и видеозаписи занятия (сценарий §6). Пусто — отчёт не
	// сформирован.
	Report string

	// Outcome — итог занятия, записанный при его закрытии. Считать
	// его заново по карточке нельзя: маршрут комнаты со временем
	// меняют, а зачёт выставлен по тому, что было в тот день
	// (сценарий §6).
	Outcome Outcome

	// Reason — причина досрочного завершения: авария или решение
	// администратора. Пусто — занятие прошло до конца.
	Reason string

	// Счётчики на момент открытия и закрытия занятия.
	AtStart  Counters
	AtFinish Counters
}

// Target — задание на тренажёр.
//
// Задаёт администратор перед упражнением или по ходу: цель относится
// к тренажёру, а не к человеку, — сменившийся спасатель выполняет то
// же задание (сценарий §5).
//
// Велоэргометр и лестница зачитываются **по времени**: спасатель
// работает отведённый срок, а метры и этажи показываются для
// наблюдения и в зачёт не идут. У ударного молота зачёт по подъёмам.
type Target struct {
	// Duration — отведённое время упражнения. Основной вид задания
	// для велоэргометра и лестницы.
	Duration time.Duration

	// Reps — подъёмов груза на ударном молоте. Единственный зачёт по
	// количеству: велоэргометр и лестница зачитываются по времени.
	// Сами подъёмы считает плата и присылает телеметрией, оператор
	// их не вводит.
	Reps uint16
}

// Set сообщает, что задание задано.
func (t Target) Set() bool { return t.Duration > 0 || t.Reps > 0 }

// Left возвращает, сколько времени осталось до конца упражнения.
// Отрицательных значений не бывает: таймер на экране не уходит в минус.
func (t Target) Left(elapsed time.Duration) time.Duration {
	if t.Duration <= 0 {
		return 0
	}
	if left := t.Duration - elapsed; left > 0 {
		return left
	}
	return 0
}

// Done сообщает, выполнено ли задание.
//
// Сравнивается только то, что задано: срок ничего не говорит о
// подъёмах, и требовать их заодно было бы неверно.
func (t Target) Done(c Counters, elapsed time.Duration) bool {
	if !t.Set() {
		return false
	}
	if t.Duration > 0 && elapsed < t.Duration {
		return false
	}
	// Подъёмы считает плата: каждое завершённое движение груза —
	// событие подхода. Поле Reps платы означает повторы внутри
	// текущего подхода и для зачёта ничего не говорит.
	if t.Reps > 0 && c.Sets < t.Reps {
		return false
	}
	return true
}

// Rotations — сколько раз звено сменило тренажёры за занятие.
//
// Нужно отчёту: по одному числу видно, прошло ли звено круг целиком
// или занятие закончилось на первом же задании.
func (s Session) Rotations() int {
	// Назначения не удаляются, и каждая смена добавляет по записи на
	// каждый тренажёр. Считаем по первому тренажёру: круг двигается
	// целиком, и по одному из них видно число кругов.
	first, n := "", 0
	for _, a := range s.Assignments {
		if first == "" {
			first = a.Device
		}
		if a.Device == first {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	// Первая посадка — не смена.
	return n - 1
}

// TaskMark — отметка о прохождении задания.
type TaskMark struct {
	Done bool
	At   time.Time
	// By — кто отметил. Пусто при автоматической отметке.
	By string
	// Auto — отметка поставлена по сигналу устройства, а не человеком.
	Auto bool
}

// TaskDone сообщает, пройдено ли задание.
func (s Session) TaskDone(id string) bool {
	return s.Tasks[id].Done
}

// TasksDone считает, сколько заданий пройдено.
func (s Session) TasksDone() int {
	var n int
	for _, m := range s.Tasks {
		if m.Done {
			n++
		}
	}
	return n
}

// Temp — температура помещения за время занятия.
//
// Хранится сводкой, а не рядом показаний: в карточке смотрят, в каких
// условиях человек работал, а подробный ход есть в истории телеметрии.
type Temp struct {
	MinC, MaxC, AvgC float64
	// Samples — по скольким показаниям посчитано. Ноль означает, что
	// данных не было: нулевые градусы и молчащий датчик на экране
	// выглядят одинаково, если их не различать.
	Samples int
}

// Known сообщает, что температура известна.
func (t Temp) Known() bool { return t.Samples > 0 }

// Record — видеозапись занятия с одной камеры.
type Record struct {
	Camera string
	// File — путь внутри хранилища записей, без ведущей косой черты.
	File     string
	Started  time.Time
	Finished time.Time
	Bytes    int64
	// Error — почему запись не удалась. Пусто — всё в порядке.
	Error string
}

// OK сообщает, что запись получилась.
func (r Record) OK() bool { return r.Error == "" && r.Bytes > 0 }

// Duration — сколько длилась запись.
func (r Record) Duration() time.Duration {
	if r.Finished.IsZero() {
		return 0
	}
	return r.Finished.Sub(r.Started)
}

// Assignment — кто занимает тренажёр.
//
// Показания датчика закрепляются за карточкой того, кто назначен
// (сценарий теплокамеры §4). При смене тренажёров запись закрывается,
// а новая открывается: результат завершённого упражнения остаётся за
// прежним участником.
type Assignment struct {
	Device string
	IIN    string

	Since time.Time
	// Until — когда назначение закрылось. Нулевое — спасатель на
	// тренажёре прямо сейчас.
	Until time.Time

	// AtStart — показания тренажёра в момент назначения: результат
	// упражнения считается как прирост, счётчики накопительные.
	AtStart Counters
	// AtEnd — показания в момент закрытия назначения.
	AtEnd Counters
}

// Running сообщает, что спасатель на тренажёре сейчас.
func (a Assignment) Running() bool { return a.Until.IsZero() }

// Duration — сколько длилось упражнение.
func (a Assignment) Duration() time.Duration {
	if a.Running() {
		return time.Since(a.Since)
	}
	return a.Until.Sub(a.Since)
}

// Result — что сделано на этом тренажёре: прирост счётчиков.
func (a Assignment) Result() Counters {
	if a.Running() {
		return Counters{}
	}
	return a.AtEnd.Sub(a.AtStart)
}

// Target возвращает задание тренажёра.
func (s Session) Target(device string) Target {
	return s.Targets[device]
}

// Busy сообщает, занят ли тренажёр прямо сейчас.
func (s Session) Busy(device string) bool {
	for _, a := range s.Assignments {
		if a.Device == device && a.Running() {
			return true
		}
	}
	return false
}

// Occupies возвращает тренажёр, на котором спасатель сейчас.
func (s Session) Occupies(iin string) (string, bool) {
	for _, a := range s.Assignments {
		if a.IIN == iin && a.Running() {
			return a.Device, true
		}
	}
	return "", false
}

// Current возвращает действующие назначения.
func (s Session) Current() []Assignment {
	out := make([]Assignment, 0, len(s.Assignments))
	for _, a := range s.Assignments {
		if a.Running() {
			out = append(out, a)
		}
	}
	return out
}

// InRoom сообщает, что комната входит в маршрут занятия.
func (s Session) InRoom(room string) bool {
	if s.Room == room {
		return true
	}
	for _, r := range s.Rooms {
		if r == room {
			return true
		}
	}
	return false
}

// Size — сколько человек в команде.
func (s Session) Size() int { return len(s.Rescuers) }

// TeamName возвращает название команды для экрана и журнала.
//
// Без названия команда обозначается составом: «звено из троих» ничего
// не говорит, а перечень участников опознаётся сразу.
func (s Session) TeamName() string {
	if s.Team != "" {
		return s.Team
	}
	return string(s.Examinee)
}

// HasRescuer сообщает, что карточка участвовала в занятии.
func (s Session) HasRescuer(iin string) bool {
	for _, r := range s.Rescuers {
		if r == iin {
			return true
		}
	}
	return false
}

// Active сообщает, что занятие ещё идёт.
func (s Session) Active() bool { return s.Finished.IsZero() }

// Interrupted сообщает, что занятие завершено досрочно — аварией или
// решением администратора. Причина записана в Reason.
func (s Session) Interrupted() bool { return !s.Active() && s.Reason != "" }

// Duration — сколько занятие длится или длилось.
func (s Session) Duration() time.Duration {
	if s.Active() {
		return time.Since(s.Started)
	}
	return s.Finished.Sub(s.Started)
}

// Result — что экзаменуемый сделал за занятие.
func (s Session) Result() Counters {
	if s.Active() {
		return Counters{}
	}
	return s.AtFinish.Sub(s.AtStart)
}

// Outcome — итог занятия.
type Outcome string

const (
	// OutcomeSuccess — маршрут пройден полностью.
	OutcomeSuccess Outcome = "success"

	// OutcomeDone — занятие проведено и закрыто администратором.
	// Зачётных условий не ставили: ни маршрута, ни заданий
	// тренажёрам, — и объявлять такое занятие незавершённым неверно.
	OutcomeDone Outcome = "done"

	// OutcomePartial — зачётные условия были, но остались
	// невыполненными: не пройден маршрут или не добрана норма.
	OutcomePartial Outcome = "partial"

	// OutcomeAborted — занятие прервано решением администратора.
	OutcomeAborted Outcome = "aborted"

	// OutcomeAlarm — занятие прервано аварийной остановкой. Отметка
	// в карточке именно такая: по сценарию §7 аварию отличают от
	// обычного досрочного завершения.
	OutcomeAlarm Outcome = "alarm"
)

// Status возвращает итоговый статус занятия словами — тот, что
// попадает в карточку участника.
func (s Session) Status() string {
	if s.Finished.IsZero() {
		return "Идёт"
	}
	switch s.Outcome {
	case OutcomeSuccess:
		return "Выполнена успешно"
	case OutcomeDone:
		return "Завершена"
	case OutcomeAlarm:
		return "Аварийная остановка"
	case OutcomeAborted:
		return "Прервана"
	case OutcomePartial:
		return "Не выполнена"
	}
	// Занятия, закрытые до появления итога, различаются по причине:
	// переписывать их статус задним числом неверно.
	if s.Reason != "" {
		return "Прервана"
	}
	return "Завершена"
}

// TaskElapsed возвращает, через сколько после старта было отмечено
// задание. Время прохождения считают от начала тренировки, а не от
// предыдущей отметки: по сценарию в карточку идёт именно оно.
func (s Session) TaskElapsed(id string) time.Duration {
	m, ok := s.Tasks[id]
	if !ok || !m.Done || m.At.IsZero() || m.At.Before(s.Started) {
		return 0
	}
	return m.At.Sub(s.Started)
}
