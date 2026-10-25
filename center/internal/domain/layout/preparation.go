package layout

import "time"

// Подготовка помещения перед тренировкой (сценарий §3).
//
// Теплокамеру греют до цели, и подготовка ограничена временем: если
// за отведённый срок температура не набрана, об этом говорят вслух, а
// не ждут бесконечно. Время подготовки в зачёт тренировки не входит —
// оно и не может туда попасть: занятие открывается отдельно.

const (
	// DefaultTargetC — целевая температура теплокамеры.
	DefaultTargetC = 40.0
	// DefaultPrepare — отведённое на прогрев время.
	DefaultPrepare = 30 * time.Minute
)

// Prepare — настройки подготовки комнаты.
type Prepare struct {
	// TargetC — целевая температура. Ноль означает, что цели нет:
	// у дымокамеры температура показывается для контроля, а готовность
	// подтверждает администратор (сценарий дымокамеры §2).
	TargetC float64
	// Timeout — сколько отведено на прогрев. Ноль — без предела.
	Timeout time.Duration
}

// Set сообщает, что подготовка настроена.
func (p Prepare) Set() bool { return p.TargetC > 0 || p.Timeout > 0 }

// Preparation — идущая подготовка комнаты.
type Preparation struct {
	TargetC float64
	Started time.Time
	// Until — когда истекает отведённое время. Нулевое — без предела.
	Until time.Time
	// Reached — когда температура впервые достигла цели. Нулевое —
	// ещё не достигала.
	Reached time.Time
}

// Running сообщает, что подготовка начата.
func (p Preparation) Running() bool { return !p.Started.IsZero() }

// Done сообщает, что цель достигнута.
func (p Preparation) Done() bool { return !p.Reached.IsZero() }

// Left возвращает, сколько времени осталось. Отрицательных значений
// не бывает: на экране таймер не уходит в минус.
func (p Preparation) Left(now time.Time) time.Duration {
	if p.Until.IsZero() {
		return 0
	}
	if left := p.Until.Sub(now); left > 0 {
		return left
	}
	return 0
}

// Expired сообщает, что отведённое время вышло, а цель не достигнута.
func (p Preparation) Expired(now time.Time) bool {
	return p.Running() && !p.Done() && !p.Until.IsZero() && now.After(p.Until)
}

// Message — что показать администратору о ходе подготовки.
func (p Preparation) Message(now time.Time) string {
	switch {
	case !p.Running():
		return ""
	case p.Done():
		// Статус «Готова» при этом не выставляется сам: его
		// подтверждает администратор, осмотрев комнату.
		return "Комната готова"
	case p.Expired(now):
		return "Температура не достигнута"
	default:
		return "Идёт прогрев"
	}
}
