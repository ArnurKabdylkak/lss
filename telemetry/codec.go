package telemetry

import (
	"encoding/json"
	"fmt"
)

// DecodeDatagram разбирает датаграмму, присланную прошивкой STM32F767
// (см. src/main.c в firmware). Формат на проводе совпадает с JSON-тегами
// Telemetry, поэтому отдельная структура не нужна.
func DecodeDatagram(raw []byte) (Telemetry, error) {
	var t Telemetry
	if err := json.Unmarshal(raw, &t); err != nil {
		return Telemetry{}, fmt.Errorf("разбор JSON: %w", err)
	}
	return t, nil
}

// EncodeDatagram собирает датаграмму в том же виде, в каком её шлёт плата.
// Нужен симуляторам и тестам.
func EncodeDatagram(t Telemetry) ([]byte, error) {
	return json.Marshal(t)
}
