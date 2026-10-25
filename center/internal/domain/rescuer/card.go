// Package rescuer — карточка спасателя: кто проходит тренировки.
//
// Карточка заводится один раз и живёт дольше любой тренировки: к ней
// привязывается история занятий, результаты с датчиков и видеозаписи.
// Опознаётся спасатель по ИИН — он уникален и не меняется, в отличие
// от фамилии.
package rescuer

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

var (
	// ErrNoIIN — карточка без ИИН не заводится: по нему идёт опознание.
	ErrNoIIN = errors.New("не указан ИИН")

	// ErrBadIIN — в поле ИИН попало то, что идентификатором быть не может.
	ErrBadIIN = errors.New("ИИН записан неверно")

	// ErrNoName — карточка без ФИО бессмысленна: по одному ИИН
	// инструктор спасателя не опознает.
	ErrNoName = errors.New("не указано ФИО")

	// ErrBadName — ФИО не похоже на имя.
	ErrBadName = errors.New("ФИО записано неверно")

	// ErrDuplicate — карточка с таким ИИН уже заведена.
	ErrDuplicate = errors.New("карточка с таким ИИН уже существует")

	// ErrNotFound — карточки с таким ИИН нет.
	ErrNotFound = errors.New("карточка не найдена")

	// ErrArchived — карточка выведена из обращения: назначать её на
	// занятие нельзя, новые результаты писать некуда.
	ErrArchived = errors.New("карточка в архиве")
)

const (
	// MaxNameLen — предел длины ФИО. Длиннее — это уже не имя.
	MaxNameLen = 120
	// MaxIINLen — предел длины ИИН. Формат не проверяется, но поле
	// остаётся идентификатором, а не местом для произвольного текста.
	MaxIINLen = 32
)

// IIN — идентификатор спасателя.
//
// Формат намеренно не проверяется: номера заносятся вручную и в
// произвольном виде. Центр относится к ИИН как к ключу — требует
// только непустоту и уникальность.
type IIN string

// NormalizeIIN убирает пробелы по краям и внутри.
//
// Иначе «123 456» и «123456» заведут две карточки на одного человека,
// а повтор ИИН по условию недопустим.
func NormalizeIIN(s string) IIN {
	return IIN(strings.Join(strings.Fields(s), ""))
}

// Validate проверяет только то, без чего ИИН перестаёт быть ключом.
func (i IIN) Validate() error {
	s := string(i)
	if s == "" {
		return ErrNoIIN
	}
	if len([]rune(s)) > MaxIINLen {
		return fmt.Errorf("%w: длиннее %d символов", ErrBadIIN, MaxIINLen)
	}
	for _, r := range s {
		// Управляющие символы в ключе означают, что в поле попало не то,
		// что набирали: они невидимы и ломают поиск и выгрузку.
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: недопустимый символ", ErrBadIIN)
		}
	}
	return nil
}

// Name — ФИО спасателя.
type Name string

// NormalizeName убирает лишние пробелы: ФИО набирают на ходу, и
// двойные пробелы превращают одного человека в двух разных в списке.
func NormalizeName(s string) Name {
	return Name(strings.Join(strings.Fields(s), " "))
}

// Validate проверяет ФИО мягко: фамилии бывают с дефисом и апострофом,
// отчества может не быть. Отсекается пустое, слишком длинное и то,
// в чём нет букв.
func (n Name) Validate() error {
	s := string(n)
	if s == "" {
		return ErrNoName
	}
	if len([]rune(s)) > MaxNameLen {
		return fmt.Errorf("%w: длиннее %d символов", ErrBadName, MaxNameLen)
	}

	var letters int
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsSpace(r), r == '-', r == '\'', r == '.':
		default:
			return fmt.Errorf("%w: недопустимый символ %q", ErrBadName, r)
		}
	}
	if letters < 2 {
		return fmt.Errorf("%w: в имени нет букв", ErrBadName)
	}
	return nil
}

// Card — карточка спасателя.
type Card struct {
	IIN  IIN
	Name Name

	// Unit — подразделение; необязательно, но в архиве по нему ищут.
	Unit string

	Created time.Time
	// Author — кто завёл карточку, из сессии администратора.
	Author string

	// Archived — карточка выведена из обращения. Не удаляется:
	// к ней привязаны прошлые тренировки, и удаление порвало бы архив.
	Archived bool
}

// Validate проверяет карточку целиком.
func (c Card) Validate() error {
	if err := c.IIN.Validate(); err != nil {
		return err
	}
	return c.Name.Validate()
}
