package telemetry

import (
	"errors"
	"time"
)

// Контракт сборщик → центр (ARCHITECTURE.md §2.2).
//
// Сборщик копит принятые пакеты и выгружает их пачкой. Центр обязан
// отбрасывать повтор уже принятого BatchID: сборщик переотправляет
// батч, если не дождался подтверждения, и дублей быть не должно.

// MaxBatchPackets ограничивает размер пачки, чтобы после долгого разрыва
// связи выгрузка не пришла одним неподъёмным запросом.
const MaxBatchPackets = 500

var (
	ErrNoBatchID    = errors.New("батч без идентификатора")
	ErrBatchTooBig  = errors.New("в батче слишком много пакетов")
	ErrNoDeviceID   = errors.New("запись без идентификатора устройства")
	ErrNoReceivedAt = errors.New("запись без времени приёма")
)

// IsPoll сообщает, что это опрос команд, а не выгрузка данных.
func (b Batch) IsPoll() bool { return len(b.Packets) == 0 }

// Batch — одна выгрузка сборщика.
type Batch struct {
	SiteID  string   `json:"site_id"`
	BatchID string   `json:"batch_id"`
	Packets []Record `json:"packets"`

	// CollectorID — кто выгружает. На объекте сборщиков несколько, по
	// одному на помещение, и команды в ответе должны уходить тому, к
	// кому подключено оборудование: иначе первый опросивший заберёт
	// чужие команды, а у соседа они просто пропадут.
	//
	// Пусто — объект с одним сборщиком: центр отдаёт всё, как раньше.
	CollectorID string `json:"collector_id,omitempty"`
}

// Record — принятый сборщиком пакет с проставленной идентичностью.
//
// Время приёма ставит сборщик, а не центр: между измерением и доставкой
// может пройти сколько угодно времени, если канал был разорван.
type Record struct {
	DeviceID string    `json:"device_id"`
	Received time.Time `json:"received"`
	Source   string    `json:"source"`
	Data     Telemetry `json:"data"`
}

// Validate проверяет батч целиком до того, как хоть одна запись попадёт
// в тракт: полупринятый батч сборщику пришлось бы слать заново, и часть
// записей задвоилась бы.
func (b Batch) Validate() error {
	if b.BatchID == "" {
		return ErrNoBatchID
	}
	// Пустая выгрузка — это опрос команд.
	//
	// Сборщик обращается к центру, даже когда выгружать нечего:
	// команды доезжают только в ответе на его запрос, и молчащие
	// платы не должны означать, что управление перестало работать.
	if len(b.Packets) > MaxBatchPackets {
		return ErrBatchTooBig
	}
	for _, r := range b.Packets {
		if r.DeviceID == "" {
			return ErrNoDeviceID
		}
		if r.Received.IsZero() {
			return ErrNoReceivedAt
		}
		if err := r.Data.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// BatchResult — ответ центра на выгрузку.
type BatchResult struct {
	Accepted  int       `json:"accepted"`            // сколько записей принято
	Duplicate bool      `json:"duplicate"`           // батч уже был принят раньше
	Commands  []Command `json:"commands,omitempty"`  // что отдать устройствам
	Error     string    `json:"error,omitempty"`
}
