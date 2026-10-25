package main

import (
	"testing"
)

func TestParseTelemetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Telemetry
		wantErr bool
	}{
		{
			name:  "пакет с платы",
			input: `{"rpm":1500.0,"period_us":40000,"pulses":372,"temp_c":23.50,"press_pa":101325,"uptime_ms":74400}`,
			want: Telemetry{RPM: 1500.0, PeriodUS: 40000, Pulses: 372,
				TempC: 23.5, PressPa: 101325, UptimeMS: 74400},
		},
		{
			name:  "вал остановлен",
			input: `{"rpm":0.0,"period_us":0,"pulses":372,"temp_c":-5.25,"press_pa":99000,"uptime_ms":80000}`,
			want: Telemetry{RPM: 0, PeriodUS: 0, Pulses: 372,
				TempC: -5.25, PressPa: 99000, UptimeMS: 80000},
		},
		{
			name:  "событие клавиатуры",
			input: `{"event":"key","key":"A","uptime_ms":90000}`,
			want:  Telemetry{Event: "key", Key: "A", UptimeMS: 90000},
		},
		{
			name:  "событие реле",
			input: `{"event":"relay","state":"off","reason":"overtemp","temp_c":41.20,"limit_c":40.00,"uptime_ms":95000}`,
			want: Telemetry{Event: "relay", State: "off", Reason: "overtemp",
				TempC: 41.2, LimitC: 40, UptimeMS: 95000},
		},
		{
			name:    "обрезанная датаграмма",
			input:   `{"rpm":1500.0,"period`,
			wantErr: true,
		},
		{
			name:    "не JSON",
			input:   "rpm=1500",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTelemetry([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseTelemetry(%q) = %+v, ожидалась ошибка", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTelemetry(%q) вернул ошибку: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseTelemetry(%q) = %+v, ожидалось %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEventKind(t *testing.T) {
	t.Parallel()

	key := Telemetry{Event: "key", Key: "A"}
	relay := Telemetry{Event: "relay", State: "off"}
	sensor := Telemetry{RPM: 1500}

	if !key.IsKeyEvent() || key.IsRelayEvent() || key.IsSensorReading() {
		t.Error("событие клавиатуры определено неверно")
	}
	if !relay.IsRelayEvent() || relay.IsKeyEvent() || relay.IsSensorReading() {
		t.Error("событие реле определено неверно")
	}
	if !sensor.IsSensorReading() || sensor.IsKeyEvent() || sensor.IsRelayEvent() {
		t.Error("показания датчиков определены неверно")
	}
}
