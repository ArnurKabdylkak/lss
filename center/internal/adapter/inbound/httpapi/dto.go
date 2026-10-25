package httpapi

import (
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Представления для API держатся отдельно от модели: формат ответа
// меняется по требованиям панели, модель — нет.

type deviceDTO struct {
	Device   string    `json:"device"`
	Received time.Time `json:"received"`
	Source   string    `json:"source,omitempty"`
	Event    string    `json:"event"`

	// Велосипед: скорость — то, что смотрит инструктор, остальное
	// оставлено для разбора, когда скорость выглядит неправдоподобно.
	SpeedKMH  float64 `json:"speed_kmh"`
	DistanceM float64 `json:"distance_m"`
	RPM       float64 `json:"rpm"`
	PeriodUS  uint32  `json:"period_us"`
	Pulses    uint32  `json:"pulses"`

	TempC   float64 `json:"temp_c"`
	PressPa float64 `json:"press_pa"`
	Key     string  `json:"key,omitempty"`

	// Препятствия: маска взведённых и сколько их всего на плате.
	Obstacles     uint32 `json:"obstacles"`
	ObstacleCount uint8  `json:"obstacle_count"`
	ObstacleIndex uint8  `json:"obstacle_index,omitempty"`
	ObstacleState string `json:"obstacle_state,omitempty"`

	// Тепловая пушка (устройство сборщика).
	State  string  `json:"relay_state,omitempty"`
	Reason string  `json:"relay_reason,omitempty"`
	LimitC float64 `json:"limit_c,omitempty"`

	// Результат проверки кода со стойки. Сам код не передаётся.
	CodeResult string `json:"code_result,omitempty"`

	Camera string `json:"camera,omitempty"`

	// Лестница и силовая установка — черновик контракта: прошивка их
	// пока не шлёт, данные даёт имитатор (см. telemetry).
	Floor     uint8   `json:"floor,omitempty"`
	Floors    uint16  `json:"floors,omitempty"`
	FloorDir  string  `json:"floor_dir,omitempty"`
	StepCount uint16  `json:"step_count,omitempty"`
	LoadKG    float64 `json:"load_kg,omitempty"`
	Reps      uint16  `json:"reps,omitempty"`
	Sets      uint16  `json:"sets,omitempty"`

	UptimeMS uint32 `json:"uptime_ms"`
}

func toDTO(p telemetry.Packet) deviceDTO {
	source := ""
	if p.From.IsValid() {
		source = p.From.String()
	}
	return deviceDTO{
		Device:        string(p.Device),
		Received:      p.Received,
		Source:        source,
		Event:         p.Data.EventName(),
		Floor:         p.Data.Floor,
		Floors:        p.Data.Floors,
		FloorDir:      p.Data.FloorDir,
		StepCount:     p.Data.StepCount,
		LoadKG:        p.Data.LoadKG,
		Reps:          p.Data.Reps,
		Sets:          p.Data.Sets,
		SpeedKMH:      p.Data.SpeedKMH,
		DistanceM:     p.Data.DistanceM,
		RPM:           p.Data.RPM,
		PeriodUS:      p.Data.PeriodUS,
		Pulses:        p.Data.Pulses,
		TempC:         p.Data.TempC,
		PressPa:       p.Data.PressPa,
		Key:           p.Data.Key,
		Obstacles:     p.Data.Obstacles,
		ObstacleCount: p.Data.ObstacleCount,
		ObstacleIndex: p.Data.ObstacleIndex,
		ObstacleState: p.Data.ObstacleState,
		State:         p.Data.State,
		Reason:        p.Data.Reason,
		LimitC:        p.Data.LimitC,
		CodeResult:    p.Data.CodeResult,
		Camera:        p.Data.Camera,
		UptimeMS:      p.Data.UptimeMS,
	}
}

func toDTOs(packets []telemetry.Packet) []deviceDTO {
	out := make([]deviceDTO, 0, len(packets))
	for _, p := range packets {
		out = append(out, toDTO(p))
	}
	return out
}

// commandDTO показывает команду вместе с её судьбой: панели важно
// отличить «не доставлена» от «доставлена и отклонена защитой».
type commandDTO struct {
	ID       string    `json:"id"`
	Device   string    `json:"device"`
	Kind     string    `json:"kind"`
	Operator string    `json:"operator,omitempty"`
	Issued   time.Time `json:"issued"`
	Attempts int       `json:"attempts"`
	Status   string    `json:"status"` // pending | delivered | rejected
	Reason   string    `json:"reason,omitempty"`
	State    string    `json:"relay_state,omitempty"`
}

func toCommandDTO(c port.CommandState) commandDTO {
	// Sanitized убирает новый код: журнал действий читают с экрана,
	// и секрету там не место.
	cmd := c.Command.Sanitized()

	dto := commandDTO{
		ID:       cmd.ID,
		Device:   string(cmd.Device),
		Kind:     string(cmd.Kind),
		Operator: cmd.Operator,
		Issued:   c.Issued,
		Attempts: c.Attempts,
		Status:   "pending",
	}
	if c.Ack != nil {
		dto.Reason = c.Ack.Reason
		dto.State = c.Ack.State
		dto.Status = "rejected"
		if c.Ack.Accepted {
			dto.Status = "delivered"
		}
	}
	return dto
}
