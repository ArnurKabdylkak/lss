package rooms

import (
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// Выдержка и период берутся из раскладки, а не из кода: это свойство
// монтажа, и инженер правит его там же, где описывает узел.
func TestРаскладкаЗадаётВыдержкуИПериод(t *testing.T) {
	room := layout.Room{
		ID: "room-3",
		Equipment: []layout.UnitSpec{
			{Unit: "rubble", Alarm: "keep", Hold: 3 * time.Second},
			{Unit: "flood", Prepare: true, Cycle: 10 * time.Second},
			{Unit: "light"},
		},
	}

	units := room.Units("")
	if len(units) != 3 {
		t.Fatalf("узлов разобрано %d", len(units))
	}

	byID := map[string]layout.Unit{}
	for _, u := range units {
		byID[u.ID] = u
	}

	if byID["rubble"].Hold != 3*time.Second {
		t.Errorf("выдержка завалов: %v", byID["rubble"].Hold)
	}
	if byID["flood"].Cycle != 10*time.Second {
		t.Errorf("период прожекторов: %v", byID["flood"].Cycle)
	}
	if byID["light"].Hold != 0 || byID["light"].Cycle != 0 {
		t.Error("у обычного узла появилась автоматика")
	}
	if !hasAutomation(units) {
		t.Error("автоматика в комнате не найдена")
	}
	if hasAutomation([]layout.Unit{byID["light"]}) {
		t.Error("автоматика найдена там, где её нет")
	}
}

// Автоматические переключения отличаются от действий человека: по
// журналу должно быть видно, кто погасил прожектор.
func TestАвторАвтоматикиОтличимОтЧеловека(t *testing.T) {
	if !Auto(authorAuto) {
		t.Error("автоматика не распознана по автору")
	}
	if Auto("Дежурный оператор (operator)") {
		t.Error("человек принят за автоматику")
	}
}
