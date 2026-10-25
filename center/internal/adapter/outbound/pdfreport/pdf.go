package pdfreport

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// build рисует отчёт о тренировке.
//
// Состав отчёта задан сценарием §6: дата и время, звено, общая
// продолжительность, задания с временем и способом подтверждения,
// попытки кода, условия в помещении, видеозаписи и итоговый статус.
func build(sess training.Session, info port.ReportInfo) ([]byte, error) {
	doc := fpdf.New("P", "mm", "A4", "")
	doc.SetTitle("Отчёт о тренировке", true)
	doc.SetAutoPageBreak(true, 15)

	reg, err := fonts.ReadFile("fonts/LiberationSans-Regular.ttf")
	if err != nil {
		return nil, fmt.Errorf("шрифт отчёта: %w", err)
	}
	bold, err := fonts.ReadFile("fonts/LiberationSans-Bold.ttf")
	if err != nil {
		return nil, fmt.Errorf("шрифт отчёта: %w", err)
	}
	doc.AddUTF8FontFromBytes("liberation", "", reg)
	doc.AddUTF8FontFromBytes("liberation", "B", bold)

	doc.AddPage()

	doc.SetFont("liberation", "B", 16)
	doc.MultiCell(0, 8, "Отчёт о тренировке", "", "L", false)

	doc.SetFont("liberation", "", 11)
	doc.MultiCell(0, 6, sess.Started.Local().Format("02.01.2006, 15:04"), "", "L", false)
	doc.Ln(2)

	rooms := make([]string, 0, len(sess.Rooms))
	for _, r := range roomList(sess) {
		if name := info.RoomNames[r]; name != "" {
			rooms = append(rooms, name)
		} else {
			rooms = append(rooms, r)
		}
	}

	rows := [][2]string{
		{"Звено", sess.TeamName()},
		{"Помещения", join(rooms, ", ")},
		{"Продолжительность", clock(sess.Duration())},
		{"Итог", sess.Status()},
	}
	if sess.Reason != "" {
		rows = append(rows, [2]string{"Причина завершения", sess.Reason})
	}
	if sess.Operator != "" {
		rows = append(rows, [2]string{"Занятие открыл", sess.Operator})
	}
	if sess.Code != "" {
		rows = append(rows, [2]string{"Код задания «Набор чисел»", sess.Code})
	}
	if sess.Attempts > 0 {
		rows = append(rows, [2]string{"Попыток ввода кода", fmt.Sprintf("%d", sess.Attempts)})
	}
	if sess.Temp.Known() {
		rows = append(rows, [2]string{"Температура в помещении", fmt.Sprintf(
			"%.1f…%.1f °C, средняя %.1f °C", sess.Temp.MinC, sess.Temp.MaxC, sess.Temp.AvgC)})
	}
	table(doc, rows)

	// Состав звена: результат связывается с карточкой каждого.
	if len(sess.Rescuers) > 0 {
		section(doc, "Состав звена")
		for i, iin := range sess.Rescuers {
			name := info.Names[iin]
			if name == "" {
				name = iin
			}
			line := fmt.Sprintf("%d. %s", i+1, name)
			if unit := info.Units[iin]; unit != "" {
				line += ", " + unit
			}
			line += fmt.Sprintf(" (ИИН %s)", iin)
			doc.MultiCell(0, 6, line, "", "L", false)
		}
	}

	// Задания маршрута: время прохождения от старта и способ отметки.
	if len(sess.Tasks) > 0 {
		section(doc, "Задания маршрута")
		marks := make([]string, 0, len(sess.Tasks))
		for id := range sess.Tasks {
			marks = append(marks, id)
		}
		sort.Slice(marks, func(i, j int) bool {
			a, b := sess.Tasks[marks[i]], sess.Tasks[marks[j]]
			if a.At.Equal(b.At) {
				return marks[i] < marks[j]
			}
			return a.At.Before(b.At)
		})

		for _, id := range marks {
			m := sess.Tasks[id]
			name := info.TaskNames[id]
			if name == "" {
				name = id
			}
			if !m.Done {
				doc.MultiCell(0, 6, name+" — не пройдено", "", "L", false)
				continue
			}
			how := "по сигналу тренажёра"
			if !m.Auto {
				how = "отметил " + m.By
				if m.By == "" {
					how = "отметил администратор"
				}
			}
			doc.MultiCell(0, 6, fmt.Sprintf("%s — %s от начала, %s",
				name, clock(sess.TaskElapsed(id)), how), "", "L", false)
		}
	}

	// Упражнения теплокамеры: по каждому назначению — кто, сколько и
	// с каким результатом.
	if len(sess.Assignments) > 0 {
		section(doc, "Упражнения")

		// Сколько раз звено сменило тренажёры. По одному числу видно,
		// прошло ли оно круг целиком или занятие закончилось на
		// первом же задании.
		if n := sess.Rotations(); n > 0 {
			doc.MultiCell(0, 6, fmt.Sprintf("Смен тренажёров: %d", n), "", "L", false)
		}

		for _, a := range sess.Assignments {
			who := info.Names[a.IIN]
			if who == "" {
				who = a.IIN
			}
			r := a.Result()
			device := info.DeviceNames[a.Device]
			if device == "" {
				device = a.Device
			}
			// Время — основной зачёт велоэргометра и лестницы, и
			// в отчёте стоит оно.
			line := fmt.Sprintf("%s — %s, %s", device, who, clock(a.Duration()))

			// У ударного молота зачёт по подъёмам: заданное
			// оператором сверяется с тем, что насчитала плата.
			// Расхождение — главное, ради чего отчёт и читают.
			if target := sess.Target(a.Device); target.Reps > 0 {
				line += fmt.Sprintf(", подъёмов задано %d, выполнено %d",
					target.Reps, r.Sets)
				switch {
				case r.Sets >= target.Reps:
					line += " — норма выполнена"
				default:
					line += fmt.Sprintf(" — не добрано %d", target.Reps-r.Sets)
				}
			} else if r.Sets > 0 {
				line += fmt.Sprintf(", подъёмов %d", r.Sets)
			}
			if r.MaxLoadKG > 0 {
				line += fmt.Sprintf(", вес до %.1f кг", r.MaxLoadKG)
			}
			doc.MultiCell(0, 6, line, "", "L", false)
		}
	}

	// Видеозаписи лежат рядом с отчётом, в подпапке video.
	if len(sess.Records) > 0 {
		section(doc, "Видеозаписи")
		for _, rec := range sess.Records {
			if !rec.OK() {
				doc.MultiCell(0, 6, fmt.Sprintf("%s — записи нет: %s",
					rec.Camera, rec.Error), "", "L", false)
				continue
			}
			doc.MultiCell(0, 6, fmt.Sprintf("%s — video/%s, %s, %.0f МБ",
				rec.Camera, base(rec.File), clock(rec.Duration()),
				float64(rec.Bytes)/(1024*1024)), "", "L", false)
		}
	}

	if sess.Comment != "" {
		section(doc, "Комментарий администратора")
		doc.MultiCell(0, 6, sess.Comment, "", "L", false)
	}

	doc.Ln(6)
	doc.SetFont("liberation", "", 9)
	doc.MultiCell(0, 5, "Отчёт сформирован центром автоматически "+
		time.Now().Local().Format("02.01.2006 15:04")+". Занятие "+
		string(sess.ID)+".", "", "L", false)

	var buf bytes.Buffer
	if err := doc.Output(&buf); err != nil {
		return nil, fmt.Errorf("вывод отчёта: %w", err)
	}
	return buf.Bytes(), nil
}

func section(doc *fpdf.Fpdf, title string) {
	doc.Ln(4)
	doc.SetFont("liberation", "B", 12)
	doc.MultiCell(0, 7, title, "", "L", false)
	doc.SetFont("liberation", "", 11)
}

// table печатает пары «что — значение» в две колонки: отчёт читают
// глазами, и ровный левый столбец для этого важнее рамок.
func table(doc *fpdf.Fpdf, rows [][2]string) {
	doc.SetFont("liberation", "", 11)
	for _, row := range rows {
		x := doc.GetX()
		doc.SetFont("liberation", "B", 11)
		doc.CellFormat(55, 7, row[0], "", 0, "L", false, 0, "")
		doc.SetFont("liberation", "", 11)
		doc.MultiCell(0, 7, row[1], "", "L", false)
		doc.SetX(x)
	}
}

func roomList(sess training.Session) []string {
	if len(sess.Rooms) > 0 {
		return sess.Rooms
	}
	return []string{sess.Room}
}

func join(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func base(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
