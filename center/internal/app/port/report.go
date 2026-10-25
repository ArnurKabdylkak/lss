package port

import (
	"context"

	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Reports — отчёт о проведённой тренировке.
//
// По сценарию §6 итог тренировки нужен не только в панели: его
// подшивают к делу. Поэтому на каждое занятие центр складывает
// отдельную папку — PDF-отчёт и видеозапись рядом, чтобы папку можно
// было отдать целиком.
type Reports interface {
	// Write формирует папку отчёта и возвращает её путь внутри
	// хранилища, без ведущей косой черты. Ролики перемещаются в эту
	// же папку, поэтому возвращённое занятие несёт обновлённые пути
	// записей.
	Write(ctx context.Context, sess training.Session, info ReportInfo) (training.Session, error)
}

// ReportInfo — то, чего нет в самом занятии: человеческие названия
// комнат и заданий. Имена участников тоже приходят снаружи: в занятии
// лежат только ИИН.
type ReportInfo struct {
	RoomNames   map[string]string
	DeviceNames map[string]string
	TaskNames   map[string]string
	Names       map[string]string
	Units       map[string]string
}
