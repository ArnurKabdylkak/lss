package sink

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// CSV пишет телеметрию в файл, сбрасывая буфер после каждой строки:
// сервис обычно останавливают сигналом, и терять хвост нельзя.
type CSV struct {
	mu   sync.Mutex
	file *os.File
	w    *csv.Writer
}

var csvHeader = []string{"timestamp", "device", "source", "event",
	"speed_kmh", "distance_m", "rpm", "period_us", "pulses",
	"temp_c", "press_pa", "key", "obstacles", "obstacle_index", "obstacle_state",
	"code_result", "relay_state", "relay_reason", "uptime_ms"}

func NewCSV(path string) (*CSV, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("создание CSV: %w", err)
	}

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		f.Close()
		return nil, fmt.Errorf("запись заголовка: %w", err)
	}
	w.Flush()

	return &CSV{file: f, w: w}, nil
}

func (c *CSV) Consume(_ context.Context, p telemetry.Packet) error {
	// У событий нет показаний датчика, и наоборот: пустые ячейки честнее
	// нулей, которые потом не отличить от настоящего нуля.
	var speed, distance, rpm, periodUS, pulses, tempC, pressPa string
	switch {
	case p.Data.IsSensorReading():
		speed = strconv.FormatFloat(p.Data.SpeedKMH, 'f', 1, 64)
		distance = strconv.FormatFloat(p.Data.DistanceM, 'f', 1, 64)
		rpm = strconv.FormatFloat(p.Data.RPM, 'f', 1, 64)
		periodUS = strconv.FormatUint(uint64(p.Data.PeriodUS), 10)
		pulses = strconv.FormatUint(uint64(p.Data.Pulses), 10)
		tempC = strconv.FormatFloat(p.Data.TempC, 'f', 2, 64)
		pressPa = strconv.FormatFloat(p.Data.PressPa, 'f', 0, 64)
	case p.Data.IsRelayEvent():
		// В событии пушки передаётся температура, по которой принято
		// решение, и порог.
		tempC = strconv.FormatFloat(p.Data.TempC, 'f', 2, 64)
	}

	var obstacles, obstacleIndex string
	if p.Data.ObstacleCount > 0 || p.Data.IsObstacleEvent() {
		obstacles = strconv.FormatUint(uint64(p.Data.Obstacles), 10)
	}
	if p.Data.IsObstacleEvent() {
		obstacleIndex = strconv.FormatUint(uint64(p.Data.ObstacleIndex), 10)
	}

	event := string(p.Data.Event)
	if event == "" {
		event = "sensor"
	}

	source := ""
	if p.From.IsValid() {
		source = p.From.String()
	}

	record := []string{
		p.Received.Format("2006-01-02T15:04:05.000"),
		string(p.Device),
		source,
		event,
		speed, distance, rpm, periodUS, pulses, tempC, pressPa,
		p.Data.Key, obstacles, obstacleIndex, p.Data.ObstacleState,
		p.Data.CodeResult, p.Data.State, p.Data.Reason,
		strconv.FormatUint(uint64(p.Data.UptimeMS), 10),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.w.Write(record); err != nil {
		return err
	}
	c.w.Flush()
	return c.w.Error()
}

func (c *CSV) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.w.Flush()
	if err := c.w.Error(); err != nil {
		c.file.Close()
		return err
	}
	return c.file.Close()
}
