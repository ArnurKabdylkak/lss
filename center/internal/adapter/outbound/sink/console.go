// Package sink содержит потребителей принятой телеметрии.
package sink

import (
	"context"
	"fmt"
	"io"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Console печатает пакеты одной строкой — быстрый взгляд на поток.
type Console struct {
	w io.Writer
}

func NewConsole(w io.Writer) *Console { return &Console{w: w} }

func (c *Console) Consume(_ context.Context, p telemetry.Packet) error {
	_, err := fmt.Fprintln(c.w, format(p))
	return err
}

func format(p telemetry.Packet) string {
	ts := p.Received.Format("15:04:05.000")
	uptime := p.Data.Uptime()

	switch {
	case p.Data.IsRelayEvent():
		return fmt.Sprintf("%s  %-21s  RELAY %-3s reason=%-12s t=%6.2fC (порог %.1fC)  uptime=%s",
			ts, p.From, p.Data.State, p.Data.Reason, p.Data.TempC, p.Data.LimitC, uptime)

	case p.Data.IsKeyEvent():
		return fmt.Sprintf("%s  %-21s  key=%-3s %44s uptime=%s",
			ts, p.From, p.Data.Key, "", uptime)

	default:
		return fmt.Sprintf("%s  %-21s  rpm=%8.1f  pulses=%8d  t=%6.2fC  p=%7.0fPa  uptime=%s",
			ts, p.From, p.Data.RPM, p.Data.Pulses, p.Data.TempC, p.Data.PressPa, uptime)
	}
}
