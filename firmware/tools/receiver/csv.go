package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
)

// csvWriter пишет принятую телеметрию в CSV, сбрасывая буфер после
// каждой строки: приёмник обычно убивают по Ctrl+C, и терять хвост нельзя.
type csvWriter struct {
	file *os.File
	w    *csv.Writer
}

func newCSVWriter(path string) (*csvWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	w := csv.NewWriter(f)
	header := []string{"timestamp", "source", "event", "rpm", "period_us", "pulses",
		"temp_c", "press_pa", "key", "relay_state", "relay_reason", "uptime_ms"}
	if err := w.Write(header); err != nil {
		f.Close()
		return nil, fmt.Errorf("запись заголовка: %w", err)
	}
	w.Flush()

	return &csvWriter{file: f, w: w}, nil
}

func (c *csvWriter) Write(p Packet) error {
	// У событий клавиатуры нет показаний датчика, и наоборот: пустые
	// ячейки честнее нулей, которые потом не отличить от настоящего нуля.
	var rpm, periodUS, pulses, tempC, pressPa string
	if p.Data.IsSensorReading() {
		rpm = strconv.FormatFloat(p.Data.RPM, 'f', 1, 64)
		periodUS = strconv.FormatUint(uint64(p.Data.PeriodUS), 10)
		pulses = strconv.FormatUint(uint64(p.Data.Pulses), 10)
		tempC = strconv.FormatFloat(p.Data.TempC, 'f', 2, 64)
		pressPa = strconv.FormatFloat(p.Data.PressPa, 'f', 0, 64)
	} else if p.Data.IsRelayEvent() {
		// В событии реле показания датчика Холла не передаются,
		// а температура — та, что вызвала срабатывание.
		tempC = strconv.FormatFloat(p.Data.TempC, 'f', 2, 64)
	}

	event := p.Data.Event
	if event == "" {
		event = "sensor"
	}

	record := []string{
		p.Received.Format("2006-01-02T15:04:05.000"),
		p.From.String(),
		event,
		rpm,
		periodUS,
		pulses,
		tempC,
		pressPa,
		p.Data.Key,
		p.Data.State,
		p.Data.Reason,
		strconv.FormatUint(uint64(p.Data.UptimeMS), 10),
	}

	if err := c.w.Write(record); err != nil {
		return err
	}
	c.w.Flush()

	return c.w.Error()
}

func (c *csvWriter) Close() error {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		c.file.Close()
		return err
	}
	return c.file.Close()
}
