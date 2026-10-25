// Package port объявляет интерфейсы, через которые прикладной слой
// общается с внешним миром. Реализации живут в internal/adapter.
package port

import (
	"context"
	"time"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Sink — потребитель принятых пакетов: файл, лог, брокер, БД.
// Ошибка Consume не останавливает приём: приёмник не должен терять
// поток из-за одного сбойного потребителя.
type Sink interface {
	Consume(ctx context.Context, p telemetry.Packet) error
}

// Closer — потребитель, которому нужно дописать хвост при остановке.
type Closer interface {
	Close() error
}

// StateRepository хранит последнее известное состояние устройств,
// чтобы отдавать его наружу (HTTP, метрики) без чтения всего потока.
type StateRepository interface {
	Save(ctx context.Context, p telemetry.Packet) error
	Latest(ctx context.Context, id telemetry.DeviceID) (telemetry.Packet, bool, error)
	List(ctx context.Context) ([]telemetry.Packet, error)
}

// HistoryRepository отдаёт накопленную историю. Отдельно от состояния:
// состояние живёт в памяти и нужно всегда, история — только там, где
// подключено постоянное хранилище.
type HistoryRepository interface {
	History(ctx context.Context, q HistoryQuery) ([]telemetry.Packet, error)
}

// HistoryQuery — выборка истории по устройству и интервалу.
type HistoryQuery struct {
	Device     telemetry.DeviceID
	From, To   time.Time
	EventsOnly bool // только события, без потока показаний
	Limit      int
}

// PacketSource — источник пакетов. Транспорт (UDP, HTTP от сборщика)
// реализует его и отдаёт пакеты прикладному слою через канал.
type PacketSource interface {
	// Packets запускает приём и закрывает канал при остановке ctx
	// или фатальной ошибке транспорта.
	Packets(ctx context.Context) (<-chan telemetry.Packet, error)
}

// CommandQueue хранит команды, ожидающие доставки на устройства.
//
// Сборщик забирает их при обращении к центру: входящих соединений он не
// принимает, поэтому центр не может отдать команду сам (§7.2).
type CommandQueue interface {
	// Enqueue ставит команду в очередь объекта.
	Enqueue(ctx context.Context, site string, cmd telemetry.Command) error
	// Take забирает до limit команд объекта, помечая их выданными.
	// allow отбирает команды, которые можно отдать этому сборщику.
	// nil — отдать всё: объект с одним сборщиком.
	Take(ctx context.Context, site string, limit int,
		allow func(telemetry.Command) bool) ([]telemetry.Command, error)
	// Settle отмечает исход команды по подтверждению от платы и
	// возвращает саму команду: по ней видно, какой узел какого
	// помещения подтвердил сборщик. Если команда центру неизвестна
	// (например, выдана до перезапуска), второе значение — false.
	Settle(ctx context.Context, ack telemetry.Ack) (telemetry.Command, bool, error)
	// Pending возвращает команды, по которым ещё нет исхода.
	Pending(ctx context.Context, site string) ([]CommandState, error)
}

// CommandState — команда вместе с тем, что с ней стало.
type CommandState struct {
	Command  telemetry.Command
	Issued   time.Time
	Taken    time.Time
	Settled  time.Time
	Ack      *telemetry.Ack
	Attempts int
}

// Delivered сообщает, что по команде пришёл ответ платы.
func (c CommandState) Delivered() bool { return c.Ack != nil }

// Broadcaster рассылает изменения состояния подписчикам (SSE).
// Панель управления должна видеть результат команды сразу, а не
// в следующем опросе (§8.3).
type Broadcaster interface {
	Publish(p telemetry.Packet)
}
