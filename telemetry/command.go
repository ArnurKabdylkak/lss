package telemetry

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Контракт команд (ARCHITECTURE.md §7).
//
// На проводе до платы команды идут НЕ в JSON, а парами ключ=значение
// через пробел. Причина: на STM32 нет разбора JSON, и тащить его ради
// четырёх команд дороже, чем описать формат, который парсится линейным
// проходом по строке. Между панелью, центром и сборщиком тот же тип
// ездит в JSON — там ограничений нет.
//
//	cmd=heater_off id=a1b2c3 seq=7
//	cmd=set_limits id=a1b2c4 seq=8 limit_c=40.00 restore_c=37.00
//	cmd=drop id=a1b2c5 seq=9 obstacle=2
//	ack=a1b2c3 ok=1 state=off reason=ok uptime_ms=123456

// Сообщения акустики. Тексты записаны на месте, центр указывает лишь,
// какое воспроизвести.
const (
	// SoundSuccess — «Пострадавший спасён. Тренировка выполнена
	// успешно» (сценарий дымокамеры §5).
	SoundSuccess = "success"
	// SoundAlarm — «Аварийная остановка. Просим покинуть помещение
	// тренировки» (§7).
	SoundAlarm = "alarm"
)

type CommandKind string

const (
	CmdHeaterOn  CommandKind = "heater_on"  // разрешить работу тепловой пушки
	CmdHeaterOff CommandKind = "heater_off" // выключить тепловую пушку
	CmdSetLimits CommandKind = "set_limits" // уставки тепловой защиты
	CmdSetPeriod CommandKind = "set_period" // период отчётов телеметрии
	CmdSetWheel  CommandKind = "set_wheel"  // длина окружности колеса, мм
	CmdDrop      CommandKind = "drop"       // сбросить препятствие
	CmdArm       CommandKind = "arm"        // взвести препятствие обратно
	CmdSetCode   CommandKind = "set_code"   // сменить код, набираемый на стойке

	// CmdFire — зажечь или потушить имитатор очага.
	//
	// Адресуется плате очага, а не сборщику: реле и кнопка тушения
	// висят на ней. Очаг горит постоянно и гаснет кнопкой — тем, кто
	// его нашёл; этой командой его зажигают заново перед следующим
	// заходом, не посылая человека в зал.
	CmdFire CommandKind = "fire"

	// CmdAlarm — аварийная остановка. ЧЕРНОВИК контракта.
	//
	// Команда адресуется сборщику комнаты, а не отдельным исполнительным
	// устройствам: он знает свою обвязку и переводит все свои выходы в
	// безопасное состояние разом — включает светильники и вытяжку,
	// отпускает замки, снимает нагрев, дымогенераторы, стробоскопы и
	// звук, запускает оповещение.
	//
	// Это дублирование аппаратной петли, а не замена ей: нагрузку
	// снимает реле безопасности независимо от ПО и сети.
	CmdAlarm CommandKind = "alarm"

	// CmdPrepare — включить оборудование подготовки помещения.
	// ЧЕРНОВИК контракта.
	//
	// Как и авария, команда адресуется сборщику комнаты: он знает
	// свою обвязку и включает то, что положено этой комнате, —
	// светильники и пушку в теплокамере, дымогенератор, стробоскоп,
	// прожекторы и акустику в дымокамере.
	CmdPrepare CommandKind = "prepare"

	// CmdPrepareStop — выключить оборудование подготовки.
	CmdPrepareStop CommandKind = "prepare_stop"

	// CmdRubble — отпустить трубы механизма «Завалы». ЧЕРНОВИК.
	//
	// Адресуется сборщику комнаты: электромагниты механизма висят на
	// его выводах и питаются отдельной цепью от замков дверей —
	// аварийное разблокирование дверей не должно ронять трубы.
	CmdRubble CommandKind = "rubble"

	// CmdAnnounce — воспроизвести сообщение через акустику. ЧЕРНОВИК.
	//
	// Текст не передаётся: фразы записаны на месте, центр лишь
	// указывает, какую воспроизвести. Так сообщение звучит одинаково
	// и не зависит от связи с центром в момент аварии.
	CmdAnnounce CommandKind = "announce"

	// CmdEquipment — включить или выключить узел оборудования
	// помещения вручную. ЧЕРНОВИК.
	//
	// Подготовка включает всё положенное комнате разом, но по ходу
	// занятия дым приходится добавлять, прожекторы гасить, а вытяжку
	// пускать раньше срока. Поэтому к общей команде добавлена
	// поштучная: `unit` называет узел, `state` — «on» или «off».
	//
	// Узлы названы по смыслу, а не по выводам: какое реле отвечает за
	// дым, знает сборщик, и центру это знать незачем.
	CmdEquipment CommandKind = "equipment"

	// CmdAlarmReset — снятие аварии. Оборудование само не
	// возобновляется: команда лишь разрешает дальнейшую работу после
	// того, как администратор устранил причину.
	CmdAlarmReset CommandKind = "alarm_reset"
)

// Узлы оборудования помещения: то, чем оператор управляет вручную
// помимо автоматики подготовки. Названы по смыслу, а не по выводам
// сборщика: какое реле за что отвечает, знает он.
const (
	UnitSmoke  = "smoke"  // дымогенератор
	UnitFlood  = "flood"  // прожекторы
	UnitStrobe = "strobe" // стробоскоп
	UnitLight  = "light"  // рабочее освещение
	UnitFan    = "fan"    // вытяжная вентиляция
	UnitSound  = "sound"  // акустика, тренировочные звуки
	UnitHeat   = "heat"   // тепловая пушка
	UnitStairs = "stairs" // питание бесконечной лестницы

	// Узлы, которыми управляют осознанно и редко. В панели они стоят
	// рядом с остальными: на объекте восемь каналов реле, и дежурный
	// должен видеть их все, а не догадываться, какой куда заведён.
	UnitLock   = "lock"   // электромагнитные замки дверей
	UnitRubble = "rubble" // магниты механизма «Завалы»
	UnitAux    = "aux"    // резервный канал
	UnitAux2   = "aux2"   // второй резервный канал
	UnitAux3   = "aux3"   // третий резервный канал
	UnitAux4   = "aux4"   // четвёртый резервный канал
)

// MaxObstacles — сколько препятствий держит одна плата. Ограничено
// разрядностью маски в телеметрии и числом свободных выводов.
const MaxObstacles = 8

// Пределы уставок. Проверяются и здесь, и на плате: полагаться на чужую
// валидацию нельзя, а показывать оператору внятную причину надо сразу.
const (
	MinLimitC = 0.0
	MaxLimitC = 125.0 // предел диапазона BMP280

	MinPeriodMS = 50
	MaxPeriodMS = 60000

	// Длина окружности колеса: от детского 12" до 29" с запасом.
	MinWheelMM = 900
	MaxWheelMM = 2500

	// Код со стойки: короче трёх цифр подбирается мгновенно, длиннее
	// двенадцати на матричной клавиатуре не набирают.
	MinCodeLen = 3
	MaxCodeLen = 12
)

// MaxCommandLen — потолок длины команды на проводе. Буфер приёма на
// плате рассчитан на это значение.
const MaxCommandLen = 160

var (
	ErrBadCommand  = errors.New("команда не разобрана")
	ErrUnknownKind = errors.New("неизвестная команда")
)

// Command — одна команда устройству.
type Command struct {
	ID       string      `json:"id"` // применяется ровно один раз
	Device   DeviceID    `json:"device"`
	Kind     CommandKind `json:"kind"`
	Seq      uint32      `json:"seq"` // против повтора старых команд
	LimitC   float64     `json:"limit_c,omitempty"`
	RestoreC float64     `json:"restore_c,omitempty"`
	PeriodMS uint32      `json:"period_ms,omitempty"`
	WheelMM  uint32      `json:"wheel_mm,omitempty"`

	// Номер препятствия для drop и arm.
	Obstacle uint8 `json:"obstacle,omitempty"`

	// Sound — какое сообщение воспроизвести для announce.
	Sound string `json:"sound,omitempty"`

	// Unit — узел оборудования помещения, State — «on» или «off»
	// (Kind == CmdEquipment).
	Unit  string `json:"unit,omitempty"`
	State string `json:"state,omitempty"`

	// Новый код для set_code. В журнале команд не показывается:
	// см. Sanitized.
	Code string `json:"code,omitempty"`

	// Кто отдал команду — для журнала действий оператора (§7.7).
	Operator string `json:"operator,omitempty"`
}

// Validate проверяет команду до постановки в очередь.
//
// Те же правила действуют и на плате. Дублирование намеренное: плата не
// может полагаться на чужую валидацию, а оператору нужно объяснение
// сразу, а не через полсекунды отказом от железа.
func (c Command) Validate() error {
	if c.ID == "" || c.Device == "" {
		return ErrBadCommand
	}

	switch c.Kind {
	case CmdRubble:
		// Параметров нет: механизм один на комнату.
	case CmdAnnounce:
		switch c.Sound {
		case SoundSuccess, SoundAlarm:
		default:
			return fmt.Errorf("%w: неизвестное сообщение %q", ErrBadCommand, c.Sound)
		}
	case CmdEquipment:
		switch c.Unit {
		case UnitSmoke, UnitFlood, UnitStrobe, UnitLight, UnitFan, UnitSound, UnitHeat,
			UnitStairs, UnitLock, UnitRubble,
			UnitAux, UnitAux2, UnitAux3, UnitAux4:
		default:
			return fmt.Errorf("%w: неизвестный узел %q", ErrBadCommand, c.Unit)
		}
		switch c.State {
		case "on", "off":
		default:
			return fmt.Errorf("%w: состояние узла %q, ожидалось on или off",
				ErrBadCommand, c.State)
		}
	case CmdAlarm, CmdAlarmReset, CmdPrepare, CmdPrepareStop:
		// Параметров нет: состояние выходов задаёт сборщик по своей
		// обвязке, а не центр по угадыванию.
	case CmdHeaterOn, CmdHeaterOff:

	case CmdSetLimits:
		// Гистерезис обязателен: без него реле пушки щёлкает на границе
		// порога из-за шума датчика.
		if c.RestoreC >= c.LimitC {
			return fmt.Errorf("%w: температура возврата (%.1f) должна быть ниже порога (%.1f)",
				ErrBadCommand, c.RestoreC, c.LimitC)
		}
		if c.LimitC <= MinLimitC || c.LimitC > MaxLimitC {
			return fmt.Errorf("%w: порог %.1f вне диапазона датчика (%.0f–%.0f °C)",
				ErrBadCommand, c.LimitC, MinLimitC, MaxLimitC)
		}
		if c.RestoreC <= MinLimitC {
			return fmt.Errorf("%w: температура возврата должна быть выше %.0f °C",
				ErrBadCommand, MinLimitC)
		}

	case CmdSetPeriod:
		if c.PeriodMS < MinPeriodMS || c.PeriodMS > MaxPeriodMS {
			return fmt.Errorf("%w: период отчётов %d мс вне границ (%d–%d)",
				ErrBadCommand, c.PeriodMS, MinPeriodMS, MaxPeriodMS)
		}

	case CmdSetWheel:
		// Ошибка в длине окружности искажает всю скорость заезда,
		// поэтому границы узкие и намеренно «велосипедные».
		if c.WheelMM < MinWheelMM || c.WheelMM > MaxWheelMM {
			return fmt.Errorf("%w: длина окружности колеса %d мм вне границ (%d–%d)",
				ErrBadCommand, c.WheelMM, MinWheelMM, MaxWheelMM)
		}

	case CmdSetCode:
		if len(c.Code) < MinCodeLen || len(c.Code) > MaxCodeLen {
			return fmt.Errorf("%w: код должен быть длиной от %d до %d цифр",
				ErrBadCommand, MinCodeLen, MaxCodeLen)
		}
		for _, r := range c.Code {
			if r < '0' || r > '9' {
				return fmt.Errorf("%w: код набирается на цифровой клавиатуре, "+
					"в нём могут быть только цифры", ErrBadCommand)
			}
		}

	case CmdFire:
		switch c.State {
		case "on", "off":
		default:
			return fmt.Errorf("%w: состояние очага %q, ожидалось on или off",
				ErrBadCommand, c.State)
		}

	case CmdDrop, CmdArm:
		if c.Obstacle >= MaxObstacles {
			return fmt.Errorf("%w: препятствие №%d вне диапазона (0–%d)",
				ErrBadCommand, c.Obstacle, MaxObstacles-1)
		}

	default:
		return ErrUnknownKind
	}
	return nil
}

// Encode собирает команду в формат, который понимает плата.
func (c Command) Encode() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cmd=%s id=%s seq=%d", c.Kind, c.ID, c.Seq)

	switch c.Kind {
	case CmdSetLimits:
		fmt.Fprintf(&b, " limit_c=%.2f restore_c=%.2f", c.LimitC, c.RestoreC)
	case CmdSetPeriod:
		fmt.Fprintf(&b, " period_ms=%d", c.PeriodMS)
	case CmdSetWheel:
		fmt.Fprintf(&b, " wheel_mm=%d", c.WheelMM)
	case CmdEquipment:
		fmt.Fprintf(&b, " unit=%s state=%s", c.Unit, c.State)
	case CmdDrop, CmdArm:
		fmt.Fprintf(&b, " obstacle=%d", c.Obstacle)
	case CmdSetCode:
		fmt.Fprintf(&b, " code=%s", c.Code)
	case CmdFire:
		fmt.Fprintf(&b, " state=%s", c.State)
	}
	return b.String()
}

// Ack — подтверждение от платы.
//
// Accepted = false означает осознанный отказ (например, включение при
// перегреве, §7.4), а не потерю команды: причина в Reason.
type Ack struct {
	ID       string `json:"id"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
	State    string `json:"state"`
	UptimeMS uint32 `json:"uptime_ms"`
}

// ParseAck разбирает подтверждение, присланное платой.
func ParseAck(raw string) (Ack, error) {
	var a Ack
	seen := false

	for _, field := range strings.Fields(raw) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return Ack{}, ErrBadCommand
		}
		switch key {
		case "ack":
			a.ID = value
			seen = true
		case "ok":
			a.Accepted = value == "1"
		case "reason":
			a.Reason = value
		case "state":
			a.State = value
		case "uptime_ms":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return Ack{}, ErrBadCommand
			}
			a.UptimeMS = uint32(n)
		}
	}
	if !seen || a.ID == "" {
		return Ack{}, ErrBadCommand
	}
	return a, nil
}

// IsAck отличает подтверждение от телеметрии: плата шлёт их в один сокет.
func IsAck(raw []byte) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "ack=")
}

// Sanitized возвращает команду без секретов — для журнала и выдачи
// наружу. Новый код не должен попадать ни в журнал действий, ни в
// ответ панели: иначе он окажется там, где его увидят посторонние.
func (c Command) Sanitized() Command {
	if c.Code != "" {
		c.Code = "•••"
	}
	return c
}
