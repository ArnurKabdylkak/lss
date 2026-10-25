package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/ArnurKabdylkak/lss/telemetry"
)

// Buffer на диске — то, ради чего сборщик вообще стоит между платой и
// центром. Обрыв канала не должен останавливать сбор, а перезапуск
// процесса — терять накопленное.
//
// Формат — JSON Lines: дозапись одной строкой, восстановление линейным
// чтением, повреждённый хвост (обрыв питания посреди записи) отбрасывается
// без потери всего файла.
type DiskBuffer struct {
	mu   sync.Mutex
	path string
	file *os.File
	w    *bufio.Writer

	// Записи в памяти: буфер невелик, а работа с ним идёт постоянно.
	// Файл — это его копия для переживания перезапуска.
	records []telemetry.Record
	// Сколько записей уже подтверждено центром и ждёт вычистки из файла.
	acked int

	maxRecords int
	dirty      bool
}

// OpenBuffer поднимает буфер с диска.
func OpenBuffer(path string, maxRecords int) (*DiskBuffer, error) {
	b := &DiskBuffer{path: path, maxRecords: maxRecords}

	if err := b.restore(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("открытие буфера: %w", err)
	}
	b.file = f
	b.w = bufio.NewWriter(f)

	return b, nil
}

// restore читает файл. Битые строки пропускаются: обрыв питания рвёт
// последнюю запись, и это не повод терять весь буфер.
func (b *DiskBuffer) restore() error {
	f, err := os.Open(b.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("чтение буфера: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for sc.Scan() {
		var r telemetry.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		b.records = append(b.records, r)
	}
	return sc.Err()
}

// Append кладёт запись в буфер.
//
// При переполнении вытесняются самые старые ПОКАЗАНИЯ; события реле и
// клавиш не вытесняются никогда — ради них потом поднимают архив.
func (b *DiskBuffer) Append(r telemetry.Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.records = append(b.records, r)
	b.dirty = true

	if len(b.records) > b.maxRecords {
		b.evict()
	}

	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := b.w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("запись в буфер: %w", err)
	}
	return nil
}

// evict удаляет самые старые показания. Вызывается под мьютексом.
func (b *DiskBuffer) evict() {
	drop := len(b.records) - b.maxRecords

	kept := make([]telemetry.Record, 0, len(b.records))
	for _, r := range b.records {
		if drop > 0 && !r.Data.IsEvent() {
			drop--
			continue
		}
		kept = append(kept, r)
	}
	b.records = kept

	// Если событий столько, что вытеснять нечего, буфер перерастёт
	// предел — это лучше, чем потерять событие защиты. Но знать об этом
	// надо, поэтому файл перепишется и размер будет виден.
	b.dirty = true
}

// Peek возвращает до n самых старых записей, не удаляя их: удалять можно
// только после подтверждения центром.
func (b *DiskBuffer) Peek(n int) []telemetry.Record {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n > len(b.records) {
		n = len(b.records)
	}
	out := make([]telemetry.Record, n)
	copy(out, b.records[:n])
	return out
}

// Commit удаляет n самых старых записей после подтверждения центром.
func (b *DiskBuffer) Commit(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n > len(b.records) {
		n = len(b.records)
	}
	b.records = b.records[n:]
	b.acked += n

	// Файл переписывается не на каждый батч: перезапись — самая дорогая
	// операция буфера, а лишние записи в файле безвредны (при
	// восстановлении их отфильтрует центр по batch_id).
	if b.acked >= 1000 || len(b.records) == 0 {
		return b.rewrite()
	}
	return b.w.Flush()
}

// rewrite переписывает файл текущим содержимым буфера.
// Через временный файл: обрыв питания посреди перезаписи не должен
// оставить обрубок вместо буфера.
func (b *DiskBuffer) rewrite() error {
	tmp := b.path + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("перезапись буфера: %w", err)
	}

	w := bufio.NewWriter(f)
	for _, r := range b.records {
		line, err := json.Marshal(r)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()

	if err := os.Rename(tmp, b.path); err != nil {
		return fmt.Errorf("замена буфера: %w", err)
	}

	// Старый дескриптор указывает на удалённый файл — переоткрываем.
	b.file.Close()
	nf, err := os.OpenFile(b.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	b.file = nf
	b.w = bufio.NewWriter(nf)
	b.acked = 0

	return nil
}

// Len возвращает глубину буфера — первая диагностика при разборе
// «почему в центре нет данных».
func (b *DiskBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.records)
}

// Flush сбрасывает файловый буфер на диск.
//
// Отдельно от Append: fsync на каждый пакет при 5 пакетах в секунду
// на устройство изнашивает носитель и ничего не даёт — потеря последней
// секунды телеметрии не стоит такой цены.
func (b *DiskBuffer) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.dirty {
		return nil
	}
	if err := b.w.Flush(); err != nil {
		return err
	}
	b.dirty = false
	return b.file.Sync()
}

// Close дописывает хвост и закрывает файл.
func (b *DiskBuffer) Close() error {
	if err := b.Flush(); err != nil {
		b.file.Close()
		return err
	}
	return b.file.Close()
}

// openAppend — вспомогательное открытие файла на дозапись.
// Используется тестами для имитации оборванной записи.
func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
}
