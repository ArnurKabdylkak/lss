package pdfreport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/app/port"
	"github.com/ArnurKabdylkak/lss/center/internal/domain/training"
)

// Отчёт отдают целиком: PDF и ролики должны лежать в одной папке, а
// в карточке — указывать на новое место (сценарий §6).
func TestWriteMovesRecordsNextToReport(t *testing.T) {
	root := t.TempDir()

	// Ролик, записанный занятием, лежит в папке занятия.
	if err := os.MkdirAll(filepath.Join(root, "s1"), 0o755); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(root, "s1", "cam-1.mp4")
	if err := os.WriteFile(clip, []byte("кадры"), 0o644); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	sess := training.Session{
		ID:       "s1",
		Room:     "room-2",
		Rooms:    []string{"room-2", "room-3"},
		Team:     "Звено 7",
		Rescuers: []string{"900315501234"},
		Started:  started,
		Finished: started.Add(12 * time.Minute),
		Outcome:  training.OutcomeSuccess,
		Code:     "4850",
		Attempts: 2,
		Tasks: map[string]training.TaskMark{
			"maze": {Done: true, At: started.Add(time.Minute), By: "Дежурный"},
			"code": {Done: true, At: started.Add(2 * time.Minute), Auto: true},
		},
		Records: []training.Record{{
			Camera: "cam-1", File: "s1/cam-1.mp4",
			Started: started, Finished: started.Add(12 * time.Minute), Bytes: 5,
		}},
		Comment: "Замечаний нет",
	}

	out, err := Writer{Root: root}.Write(context.Background(), sess, port.ReportInfo{
		RoomNames: map[string]string{"room-2": "Дымокамера, комната 1"},
		TaskNames: map[string]string{"maze": "Лабиринт", "code": "Набор чисел"},
		Names:     map[string]string{"900315501234": "Иванов Пётр Сергеевич"},
		Units:     map[string]string{"900315501234": "СЧ-2"},
	})
	if err != nil {
		t.Fatalf("Write() вернул ошибку: %v", err)
	}

	want := "reports/2026-10-20/09-30-room-2-Звено-7"
	if out.Report != want {
		t.Errorf("папка отчёта %q, ожидалась %q", out.Report, want)
	}
	if got := out.Records[0].File; got != want+"/video/cam-1.mp4" {
		t.Errorf("путь ролика %q — он должен указывать в папку отчёта", got)
	}

	// Ролик переехал, а не скопировался: место на диске дорого.
	if _, err := os.Stat(clip); !os.IsNotExist(err) {
		t.Error("ролик остался на прежнем месте")
	}
	if _, err := os.Stat(filepath.Join(root, want, "video", "cam-1.mp4")); err != nil {
		t.Errorf("ролика нет в папке отчёта: %v", err)
	}

	pdf, err := os.ReadFile(filepath.Join(root, want, "report.pdf"))
	if err != nil {
		t.Fatalf("отчёт не записан: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Error("файл отчёта — не PDF")
	}
	if len(pdf) < 1024 {
		t.Errorf("отчёт подозрительно мал: %d байт", len(pdf))
	}
}

// Занятие без роликов тоже получает отчёт: камеры могли быть
// отключены, а зачёт всё равно подшивают.
func TestWriteWithoutRecords(t *testing.T) {
	root := t.TempDir()
	sess := training.Session{
		ID: "s2", Room: "room-1", Team: "Звено 1",
		Started:  time.Date(2026, 10, 20, 14, 5, 0, 0, time.Local),
		Finished: time.Date(2026, 10, 20, 14, 25, 0, 0, time.Local),
		Reason:   "нажата кнопка у входа",
		Outcome:  training.OutcomeAlarm,
	}

	out, err := Writer{Root: root}.Write(context.Background(), sess, port.ReportInfo{})
	if err != nil {
		t.Fatalf("Write() вернул ошибку: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, out.Report, "report.pdf")); err != nil {
		t.Errorf("отчёт не записан: %v", err)
	}
}

// Имя папки читают глазами: косые черты и прочее из названия звена
// путь ломать не должны.
func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"Звено 7":      "Звено-7",
		"СЧ-2/смена 1": "СЧ-2-смена-1",
		"  ":           "без-названия",
		`a:b*c?"<>|`:   "a-b-c",
	}
	for in, want := range cases {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
