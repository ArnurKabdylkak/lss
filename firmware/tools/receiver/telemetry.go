package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"time"
)

// Плата шлёт три вида датаграмм, они различаются полем "event"
// (см. src/main.c): периодические показания датчиков, события клавиатуры
// и смену состояния реле тепловой защиты.
const (
	eventKey   = "key"
	eventRelay = "relay"
)

// Telemetry повторяет JSON, который STM32F767 шлёт по UDP.
type Telemetry struct {
	// Event пуст для периодических показаний датчика.
	Event string `json:"event"`

	// Показания датчика Холла.
	RPM      float64 `json:"rpm"`
	PeriodUS uint32  `json:"period_us"`
	Pulses   uint32  `json:"pulses"`

	// Показания BMP280.
	TempC   float64 `json:"temp_c"`
	PressPa float64 `json:"press_pa"`

	// Нажатая клавиша, заполняется при Event == eventKey.
	Key string `json:"key"`

	// Состояние реле ("on"/"off"), причина и порог защиты —
	// заполняются при Event == eventRelay.
	State  string  `json:"state"`
	Reason string  `json:"reason"`
	LimitC float64 `json:"limit_c"`

	UptimeMS uint32 `json:"uptime_ms"`
}

// IsKeyEvent сообщает, что датаграмма описывает нажатие клавиши,
// а не очередное измерение.
func (t Telemetry) IsKeyEvent() bool {
	return t.Event == eventKey
}

// IsRelayEvent сообщает, что датаграмма описывает смену состояния реле.
func (t Telemetry) IsRelayEvent() bool {
	return t.Event == eventRelay
}

// IsSensorReading сообщает, что это очередное измерение, а не событие.
func (t Telemetry) IsSensorReading() bool {
	return t.Event == ""
}

// Packet — одна принятая датаграмма вместе с метаданными приёма.
type Packet struct {
	Received time.Time
	From     netip.AddrPort
	Data     Telemetry
}

func parseTelemetry(b []byte) (Telemetry, error) {
	var t Telemetry
	if err := json.Unmarshal(b, &t); err != nil {
		return Telemetry{}, fmt.Errorf("разбор JSON: %w", err)
	}
	return t, nil
}

// String форматирует пакет одной строкой для вывода в консоль.
func (p Packet) String() string {
	uptime := time.Duration(p.Data.UptimeMS) * time.Millisecond

	if p.Data.IsRelayEvent() {
		return fmt.Sprintf("%s  %-21s  RELAY %-3s reason=%-12s t=%6.2fC (порог %.1fC)  uptime=%s",
			p.Received.Format("15:04:05.000"),
			p.From,
			p.Data.State,
			p.Data.Reason,
			p.Data.TempC,
			p.Data.LimitC,
			uptime,
		)
	}

	if p.Data.IsKeyEvent() {
		return fmt.Sprintf("%s  %-21s  key=%-3s %44s uptime=%s",
			p.Received.Format("15:04:05.000"),
			p.From,
			p.Data.Key,
			"",
			uptime,
		)
	}

	return fmt.Sprintf("%s  %-21s  rpm=%8.1f  pulses=%8d  t=%6.2fC  p=%7.0fPa  uptime=%s",
		p.Received.Format("15:04:05.000"),
		p.From,
		p.Data.RPM,
		p.Data.Pulses,
		p.Data.TempC,
		p.Data.PressPa,
		uptime,
	)
}
