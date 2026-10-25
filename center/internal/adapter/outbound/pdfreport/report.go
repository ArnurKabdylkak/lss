// Package pdfreport складывает отчёт о тренировке на диск.
//
// Папка на занятие, а в ней PDF и видеозапись: отчёт отдают целиком —
// на флешке или в деле, — и разложенные по разным местам ролики и
// бумага рядом не лежат. Шрифт вшит в двоичный файл: образ центра
// собирается из scratch, и в нём нет ни одного системного шрифта.
package pdfreport

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

//go:embed fonts/*.ttf
var fonts embed.FS

// Writer формирует отчёты в каталоге Root.
type Writer struct {
	// Root — корень хранилища записей: отчёты складываются в
	// подкаталог reports рядом с роликами, на том же томе, иначе
	// перенос ролика стал бы копированием гигабайта.
	Root string
	Log  *slog.Logger
}

// Dir — имя подкаталога отчётов внутри хранилища.
const Dir = "reports"

// Write формирует папку отчёта и переносит в неё ролики занятия.
func (w Writer) Write(ctx context.Context, sess training.Session,
	info port.ReportInfo) (training.Session, error) {

	if w.Root == "" {
		return sess, nil
	}

	rel := folder(sess)
	abs := filepath.Join(w.Root, rel)
	if err := os.MkdirAll(filepath.Join(abs, "video"), 0o755); err != nil {
		return sess, fmt.Errorf("папка отчёта: %w", err)
	}

	// Ролики переезжают в папку отчёта: на одном томе это
	// переименование, а не копирование.
	for i := range sess.Records {
		rec := &sess.Records[i]
		if rec.File == "" || !rec.OK() {
			continue
		}
		src := filepath.Join(w.Root, filepath.FromSlash(rec.File))
		name := filepath.Base(rec.File)
		dst := filepath.Join(abs, "video", name)
		if err := os.Rename(src, dst); err != nil {
			// Отчёт важнее переноса: ролик остаётся на прежнем месте,
			// и ссылка в карточке продолжает работать.
			w.log().Error("ролик не перенесён в отчёт",
				"session", sess.ID, "file", rec.File, "err", err)
			continue
		}
		rec.File = rel + "/video/" + name
	}

	sess.Report = rel

	pdf, err := build(sess, info)
	if err != nil {
		return sess, err
	}
	if err := os.WriteFile(filepath.Join(abs, "report.pdf"), pdf, 0o644); err != nil {
		return sess, fmt.Errorf("запись отчёта: %w", err)
	}

	// Пустую папку с роликами убираем: она осталась от записи.
	_ = os.Remove(filepath.Join(w.Root, string(sess.ID)))

	w.log().Info("отчёт сформирован", "session", sess.ID, "папка", rel)
	return sess, nil
}

func (w Writer) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// folder собирает имя папки: дата, время, комната и команда. Имя
// читается глазами — отчёты ищут по дню и звену, а не по хешу.
func folder(sess training.Session) string {
	t := sess.Started.Local()
	name := fmt.Sprintf("%s-%s-%s", t.Format("15-04"), sess.Room, safe(sess.TeamName()))
	return Dir + "/" + t.Format("2006-01-02") + "/" + name
}

// safe убирает из имени то, что ломает путь или мешает его прочитать.
func safe(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "без-названия"
	}

	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteByte('-')
		case r == ' ':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len([]rune(out)) > 60 {
		out = string([]rune(out)[:60])
	}
	if out == "" {
		return "без-названия"
	}
	return out
}

func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
