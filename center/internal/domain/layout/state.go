package layout

import (
	"errors"
	"fmt"
	"time"
)

// State — состояние комнаты на главном экране.
//
// Состояния задают порядок работы: тренировка не начинается, пока
// комнату не подготовили и администратор не подтвердил готовность.
type State string

const (
	StateWaiting   State = "waiting"   // Ожидание: комната свободна
	StatePreparing State = "preparing" // Подготовка: оборудование включено, идёт прогрев
	StateReady     State = "ready"     // Готова: администратор проверил и подтвердил
	StateTraining  State = "training"  // Тренировка: идёт занятие
	StateFinishing State = "finishing" // Завершение: участники выходят, данные сохраняются
	StateAlarm     State = "alarm"     // Авария: сработала аварийная остановка
)

// ErrBadTransition — переход между состояниями не предусмотрен.
var ErrBadTransition = errors.New("недопустимый переход состояния комнаты")

// Title — название состояния для экрана.
func (s State) Title() string {
	switch s {
	case StateWaiting:
		return "Ожидание"
	case StatePreparing:
		return "Подготовка"
	case StateReady:
		return "Готова"
	case StateTraining:
		return "Тренировка"
	case StateFinishing:
		return "Завершение"
	case StateAlarm:
		return "Авария"
	default:
		return string(s)
	}
}

// Known сообщает, что состояние известно центру.
func (s State) Known() bool {
	switch s {
	case StateWaiting, StatePreparing, StateReady, StateTraining, StateFinishing, StateAlarm:
		return true
	default:
		return false
	}
}

// allowed — куда можно перейти из каждого состояния.
//
// Переход в «Аварию» разрешён отовсюду и проверяется отдельно: авария
// случается независимо от того, чем комната занята.
var allowed = map[State][]State{
	StateWaiting:   {StatePreparing},
	StatePreparing: {StateReady, StateWaiting},
	StateReady:     {StateTraining, StatePreparing, StateWaiting},
	StateTraining:  {StateFinishing},
	StateFinishing: {StateWaiting, StatePreparing},
	// Из аварии — только в ожидание, и только вручную: сброс кнопки
	// не возобновляет работу (сценарий §7).
	StateAlarm: {StateWaiting},
}

// CanMove сообщает, допустим ли переход.
func (s State) CanMove(to State) bool {
	if !to.Known() {
		return false
	}
	if to == StateAlarm {
		// Авария объявляется из любого состояния, кроме неё самой.
		return s != StateAlarm
	}
	for _, next := range allowed[s] {
		if next == to {
			return true
		}
	}
	return false
}

// Move проверяет переход и возвращает ошибку с объяснением.
func (s State) Move(to State) error {
	if !to.Known() {
		return fmt.Errorf("%w: состояние %q неизвестно", ErrBadTransition, to)
	}
	if s == to {
		return nil
	}
	if !s.CanMove(to) {
		return fmt.Errorf("%w: из «%s» в «%s» нельзя", ErrBadTransition, s.Title(), to.Title())
	}
	return nil
}

// Busy сообщает, что комната занята и свободной не считается.
func (s State) Busy() bool {
	return s == StatePreparing || s == StateReady || s == StateTraining || s == StateFinishing
}

// RoomState — состояние комнаты вместе с тем, когда и почему оно наступило.
type RoomState struct {
	Room  RoomID
	State State
	Since time.Time

	// Reason — причина перехода: для аварии обязательна, для остальных
	// переходов необязательна, но попадает в журнал.
	Reason string
	// Author — кто перевёл комнату, из сессии администратора.
	Author string

	// Prep — идущая подготовка, если комната греется. Хранится рядом
	// с состоянием: это одно и то же положение дел, и разводить их по
	// двум записям значило бы допустить их расхождение.
	Prep Preparation

	// Code — четырёхзначный код задания «Набор чисел» на эту
	// тренировку. Формируется при подготовке: администратор видит его
	// до старта и сообщает участникам (сценарий дымокамеры §4).
	Code string

	// Equipment — что из оборудования включено: узел -> включён.
	// Это **подтверждённое** состояние: узел попадает сюда, когда
	// сборщик помещения отчитался, что реле действительно щёлкнуло.
	// Хранится вместе с состоянием, иначе панель после перезапуска
	// центра показывала бы выключенным то, что работает.
	Equipment map[string]bool

	// UnitSince — когда узел последний раз переключали. По нему
	// центр отсчитывает выдержку («Завалы» снимаются через три
	// секунды) и период мигания прожекторов.
	UnitSince map[string]time.Time

	// Manual — узлы, которые трогал человек. Автоматика по ним
	// молчит до конца цикла комнаты: оператор, нажавший кнопку,
	// должен получить то, что нажал, а не борьбу с мигалкой.
	Manual map[string]bool

	// Pending — узлы, по которым команда ушла, а ответа сборщика ещё
	// нет: узел -> каким его хотят видеть. Отделено от Equipment
	// намеренно. Пульт зажигает лампу узла только по подтверждению,
	// и оптимистичная запись «включено» сразу после нажатия показала
	// бы работающим то, чего, может быть, не произошло.
	Pending map[string]bool

	// PendingSince — когда по узлу ушла команда. Нужна, чтобы
	// ожидание не висело вечно: ответ сборщика теряется — плата
	// отключена, сборщик перезапущен, пакет пропал, — и узел,
	// оставшийся «ждущим», переставал бы слушаться пульта вовсе
	// (повторное нажатие там отбрасывается до подтверждения). По
	// истечении срока ожидание снимается, а расхождение становится
	// видно как неисправность, а не как вечно мигающая лампа.
	//
	// Отдельно от UnitSince: ту сдвигает автоматика, отмечая узлы
	// без отправки команды, и срок ожидания поехал бы вместе с ней.
	PendingSince map[string]time.Time

	// Roster — состав, записанный на эту тренировку: ФИО и ИИН
	// заносят **до подготовки**, и без него помещение не готовят.
	// Прогрев и дым стоят денег и времени, а решение «кого учим»
	// принимают до того, как включать оборудование.
	Roster Roster
}

// Roster — состав, записанный на тренировку до начала подготовки.
type Roster struct {
	// Team — название звена. У теплокамеры звеньев нет: там занятие
	// идёт по тренажёрам, и поле остаётся пустым.
	Team string

	// Rescuers — ИИН карточек участников.
	Rescuers []string

	// Author — кто записал состав, Since — когда.
	Author string
	Since  time.Time
}

// Set сообщает, что состав записан.
func (r Roster) Set() bool { return len(r.Rescuers) > 0 }

// Size — сколько человек записано.
func (r Roster) Size() int { return len(r.Rescuers) }

// Alarm сообщает, что комната в аварии.
func (r RoomState) Alarm() bool { return r.State == StateAlarm }

// AlarmEvent — запись об аварийной остановке для журнала.
//
// Журнал нужен отдельно от состояний комнат: состояние показывает,
// как сейчас, а разбор требует знать, когда и почему это случилось
// (сценарий §7).
type AlarmEvent struct {
	ID     string
	At     time.Time
	Reason string
	Author string
	Rooms  []RoomID
	// Sessions — занятия, прерванные этой аварией.
	Sessions []string
	// Reset — когда авария снята; нулевое — ещё не снята.
	Reset time.Time
}

// Active сообщает, что авария ещё не снята.
func (a AlarmEvent) Active() bool { return a.Reset.IsZero() }
