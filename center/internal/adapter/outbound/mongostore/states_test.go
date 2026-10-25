package mongostore

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/layout"
)

// Схема документа в Mongo своя, и поле состояния, забытое в ней,
// теряется молча: центр сохраняет, читает — и получает пустоту.
// Один раз это уже стоило висящего ожидания на пульте, поэтому
// соответствие полей проверяется, а не держится в голове.
//
// Поля, которые переносятся вложенными документами, перечислены
// отдельно: у них другое имя в схеме, и это намеренно.
func TestДокументСостоянияНеТеряетПолей(t *testing.T) {
	nested := map[string]string{
		"Prep":   "Prep",   // prepDoc
		"Roster": "Roster", // rosterDoc
	}

	docFields := map[string]bool{}
	dt := reflect.TypeOf(stateDoc{})
	for i := 0; i < dt.NumField(); i++ {
		docFields[dt.Field(i).Name] = true
	}

	st := reflect.TypeOf(layout.RoomState{})
	for i := 0; i < st.NumField(); i++ {
		name := st.Field(i).Name
		if want, ok := nested[name]; ok {
			if !docFields[want] {
				t.Errorf("поле %s не перенесено вложенным документом", name)
			}
			continue
		}
		if !docFields[name] {
			t.Errorf("поле %s состояния отсутствует в схеме Mongo: "+
				"оно будет теряться при сохранении", name)
		}
	}
}

// Карты времён должны переживать разбор документа: именно на этом
// ломался срок ожидания — отметка уходила в базу и не возвращалась.
func TestОтметкиВремениПереживаютДокумент(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	doc := stateDoc{
		Room:         "room-1",
		State:        "preparing",
		Since:        now,
		Equipment:    map[string]bool{"light": true},
		Pending:      map[string]bool{"heat": true},
		PendingSince: map[string]time.Time{"heat": now},
		UnitSince:    map[string]time.Time{"light": now},
		Manual:       map[string]bool{"light": true},
	}

	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back stateDoc
	if err := bson.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}

	got := fromStateDoc(back)
	if _, ok := got.PendingSince["heat"]; !ok {
		t.Fatal("отметка времени ожидания потеряна при разборе документа")
	}
	if !got.PendingSince["heat"].Equal(now) {
		t.Errorf("отметка ожидания изменилась: %v вместо %v",
			got.PendingSince["heat"], now)
	}
	if !got.UnitSince["light"].Equal(now) {
		t.Errorf("отметка узла изменилась: %v вместо %v",
			got.UnitSince["light"], now)
	}
	if !got.Pending["heat"] {
		t.Error("ожидание потеряно")
	}
}
