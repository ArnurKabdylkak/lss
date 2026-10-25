// Package command — сценарии управления: постановка команд в очередь,
// выдача их сборщику и учёт исхода.
//
// Центр не может отдать команду сам: сборщик входящих соединений не
// принимает и забирает команды, когда обращается сам (§7.2).
package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// MaxPerBatch — сколько команд отдаётся сборщику за одно обращение.
const MaxPerBatch = 16

type Service struct {
	queue port.CommandQueue
	log   *slog.Logger

	// settled вызывается на каждое подтверждение с исходной командой.
	// Через него состояние помещения узнаёт, что реле действительно
	// щёлкнуло: иначе центр показывал бы желаемое, а не сущее.
	settled Observer
}

// Observer получает исход команды вместе с самой командой.
type Observer func(ctx context.Context, cmd telemetry.Command, ack telemetry.Ack)

func New(queue port.CommandQueue, log *slog.Logger) *Service {
	return &Service{queue: queue, log: log}
}

// Observe назначает наблюдателя исходов. Вызывается при сборке
// сервиса: наблюдатель знает про помещения, а команды про них не
// знают, и связывать их должен тот, кто собирает обе стороны.
func (s *Service) Observe(o Observer) { s.settled = o }

// Issue проверяет команду, присваивает ей идентификатор и ставит в очередь.
//
// Идентификатор нужен, чтобы плата применила команду ровно один раз:
// сборщик переотправляет её, не дождавшись подтверждения.
func (s *Service) Issue(ctx context.Context, site string, cmd telemetry.Command) (telemetry.Command, error) {
	if site == "" {
		return telemetry.Command{}, fmt.Errorf("объект не указан")
	}
	if cmd.ID == "" {
		cmd.ID = newID()
	}
	// Seq намеренно не заполняется: его ставит сборщик перед отправкой
	// на плату. У центра и у сборщика (досылка уставок после
	// перезапуска платы) независимые источники команд, и две сквозные
	// нумерации для одной платы разъехались бы.

	if err := cmd.Validate(); err != nil {
		return telemetry.Command{}, err
	}
	if err := s.queue.Enqueue(ctx, site, cmd); err != nil {
		return telemetry.Command{}, err
	}

	// Новый код в журнал не пишется: журналы читают и пересылают.
	s.log.Info("команда поставлена в очередь",
		"site", site, "device", cmd.Device, "kind", cmd.Kind, "id", cmd.ID,
		"operator", cmd.Operator)

	return cmd, nil
}

// Take выдаёт сборщику команды его объекта.
func (s *Service) Take(ctx context.Context, site string,
	allow func(telemetry.Command) bool) ([]telemetry.Command, error) {

	return s.queue.Take(ctx, site, MaxPerBatch, allow)
}

// Settle принимает подтверждение от платы.
//
// Отказ (Accepted = false) — это осознанное решение защиты, а не сбой
// доставки: команда считается доставленной, но неисполненной (§7.4).
func (s *Service) Settle(ctx context.Context, ack telemetry.Ack) error {
	if ack.Accepted {
		s.log.Info("команда исполнена", "id", ack.ID, "state", ack.State)
	} else {
		s.log.Warn("команда отклонена платой", "id", ack.ID, "reason", ack.Reason)
	}

	cmd, known, err := s.queue.Settle(ctx, ack)
	if err != nil {
		return err
	}
	if known && s.settled != nil {
		s.settled(ctx, cmd, ack)
	}
	return nil
}

// Pending возвращает команды объекта, по которым ещё нет исхода.
func (s *Service) Pending(ctx context.Context, site string) ([]port.CommandState, error) {
	return s.queue.Pending(ctx, site)
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
