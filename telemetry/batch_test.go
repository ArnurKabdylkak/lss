package telemetry

import (
	"testing"
	"time"
)

func goodRecord() Record {
	return Record{DeviceID: "stm32-01", Received: time.Now(), Data: Telemetry{RPM: 1500}}
}

func TestBatchValidate(t *testing.T) {
	ok := Batch{SiteID: "obj-17", BatchID: "b1", Packets: []Record{goodRecord()}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("годный батч отвергнут: %v", err)
	}

	noTime := goodRecord()
	noTime.Received = time.Time{}

	cases := map[string]Batch{
		"без идентификатора": {Packets: []Record{goodRecord()}},
		"запись без устройства": {BatchID: "b1", Packets: []Record{{
			Received: time.Now(), Data: Telemetry{RPM: 1}}}},
		"запись без времени": {BatchID: "b1", Packets: []Record{noTime}},
		"битая телеметрия": {BatchID: "b1", Packets: []Record{{
			DeviceID: "stm32-01", Received: time.Now(),
			Data: Telemetry{Event: EventKey}}}},
	}

	for name, b := range cases {
		if err := b.Validate(); err == nil {
			t.Errorf("%s: батч должен быть отвергнут", name)
		}
	}
}

// Батч проверяется целиком до приёма: иначе часть записей осела бы в
// центре, сборщик переотправил бы батч, и они задвоились.
func TestBatchValidatedAsWhole(t *testing.T) {
	b := Batch{BatchID: "b1", Packets: []Record{goodRecord(), {DeviceID: "x"}}}
	if err := b.Validate(); err == nil {
		t.Fatal("батч с одной битой записью должен быть отвергнут целиком")
	}
}

// Пустая выгрузка — это опрос команд: сборщик обращается к центру,
// даже когда выгружать нечего, иначе молчащие платы означали бы,
// что управление перестало работать.
func TestПустойБатчЭтоОпросКоманд(t *testing.T) {
	poll := Batch{SiteID: "obj-17", BatchID: "b1"}

	if err := poll.Validate(); err != nil {
		t.Fatalf("опрос команд отвергнут: %v", err)
	}
	if !poll.IsPoll() {
		t.Error("пустая выгрузка не распознана как опрос")
	}

	full := Batch{SiteID: "obj-17", BatchID: "b2", Packets: []Record{goodRecord()}}
	if full.IsPoll() {
		t.Error("выгрузка с данными принята за опрос")
	}
}
