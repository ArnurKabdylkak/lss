package rescuer

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeIIN(t *testing.T) {
	t.Parallel()

	// Пробелы убираются: иначе «123 456» и «123456» заведут две
	// карточки на одного человека.
	tests := map[string]IIN{
		"900315 501 234": "900315501234",
		" 900315501234 ": "900315501234",
		"сл-2026/17":     "сл-2026/17",
		"   ":            "",
	}
	for in, want := range tests {
		if got := NormalizeIIN(in); got != want {
			t.Errorf("NormalizeIIN(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestIINValidate(t *testing.T) {
	t.Parallel()

	// Формат не проверяется намеренно: номера заносятся вручную
	// и в произвольном виде.
	good := []IIN{"900315501234", "сл-2026/17", "A-17"}
	for _, i := range good {
		if err := i.Validate(); err != nil {
			t.Errorf("Validate(%q) вернул ошибку: %v", i, err)
		}
	}

	tests := []struct {
		name string
		in   IIN
		want error
	}{
		{"пусто", "", ErrNoIIN},
		{"длиннее предела", IIN(strings.Repeat("1", MaxIINLen+1)), ErrBadIIN},
		{"управляющий символ", "9003\n501234", ErrBadIIN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.in.Validate(); !errors.Is(err, tt.want) {
				t.Errorf("Validate(%q) = %v, ожидалось %v", tt.in, err, tt.want)
			}
		})
	}
}

func TestNameValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Name
		want error
	}{
		{"обычное ФИО", "Иванов Пётр Сергеевич", nil},
		{"без отчества", "Петров Иван", nil},
		{"с дефисом", "Мамин-Сибиряк Д.", nil},
		{"пусто", "", ErrNoName},
		{"цифры", "Иванов 1995", ErrBadName},
		{"разметка", "Иванов <b>", ErrBadName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.in.Validate()
			if tt.want == nil && err != nil {
				t.Fatalf("Validate(%q) вернул ошибку: %v", tt.in, err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Validate(%q) = %v, ожидалось %v", tt.in, err, tt.want)
			}
		})
	}
}

func TestCardValidate(t *testing.T) {
	t.Parallel()

	ok := Card{IIN: "сл-17", Name: "Иванов Пётр"}
	if err := ok.Validate(); err != nil {
		t.Errorf("корректная карточка отклонена: %v", err)
	}
	if err := (Card{Name: "Иванов Пётр"}).Validate(); !errors.Is(err, ErrNoIIN) {
		t.Error("карточка без ИИН должна отклоняться")
	}
	if err := (Card{IIN: "сл-17"}).Validate(); !errors.Is(err, ErrNoName) {
		t.Error("карточка без ФИО должна отклоняться")
	}
}
