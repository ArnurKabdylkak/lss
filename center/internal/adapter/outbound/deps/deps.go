// Package deps раскладывает рядом с программой то, без чего она не
// работает: ffmpeg и компонент просмотра веб-страниц.
//
// Всё нужное лежит внутри исполняемого файла. Оператор на объекте
// ничего не скачивает и не ставит: сети наружу там нет, прав
// администратора у него может не быть, а инструкция «доложите
// ffmpeg.exe рядом» живёт ровно до первого человека, который её не
// прочитал.
//
// Распаковка одноразовая: при следующих запусках файлы уже на месте,
// и программа открывается сразу.
package deps

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Unpacker достаёт вложенные файлы на диск.
type Unpacker struct {
	dir string
	log *slog.Logger
}

func New(dir string, log *slog.Logger) *Unpacker {
	return &Unpacker{dir: dir, log: log}
}

// File — один вложенный файл: сжатое содержимое и имя на диске.
type File struct {
	Name string
	Data []byte
	// Executable — файл должен стать исполняемым. На Windows права
	// не нужны, но программа собирается и для проверки на Linux.
	Executable bool
}

// Ensure распаковывает файл, если его ещё нет или он отличается.
//
// Отличие проверяется по размеру, а не по содержимому: считать хеш
// восьмидесяти мегабайт при каждом запуске — это секунды задержки на
// ровном месте, а подменять файл руками здесь некому.
func (u *Unpacker) Ensure(f File) (string, error) {
	path := filepath.Join(u.dir, f.Name)

	want, err := gunzipSize(f.Data)
	if err != nil {
		return "", err
	}

	if info, err := os.Stat(path); err == nil && info.Size() == want {
		return path, nil
	}

	if err := os.MkdirAll(u.dir, 0o750); err != nil {
		return "", fmt.Errorf("каталог для %s: %w", f.Name, err)
	}

	u.log.Info("распаковываю вложенный файл", "файл", f.Name,
		"размер", want, "куда", u.dir)

	// Пишем во временный файл и переименовываем: прерванная
	// распаковка иначе оставила бы обрезанный ffmpeg, который
	// запускается и сразу падает.
	tmp := path + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode(f))
	if err != nil {
		return "", fmt.Errorf("создание %s: %w", f.Name, err)
	}

	if err := gunzipTo(out, f.Data); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("запись %s: %w", f.Name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("замена %s: %w", f.Name, err)
	}

	return path, nil
}

func mode(f File) os.FileMode {
	if f.Executable {
		return 0o755
	}
	return 0o644
}

// gunzipTo распаковывает содержимое в поток.
func gunzipTo(w io.Writer, data []byte) error {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("вложенный файл повреждён: %w", err)
	}
	defer r.Close()

	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("распаковка: %w", err)
	}
	return nil
}

// gunzipSize считает размер распакованного.
func gunzipSize(data []byte) (int64, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("вложенный файл повреждён: %w", err)
	}
	defer r.Close()

	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return 0, fmt.Errorf("распаковка: %w", err)
	}
	return n, nil
}

// Sum — отпечаток содержимого. Нужен журналу: по нему видно, какая
// именно сборка вложена, когда разбираются с неполадкой на объекте.
func Sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}
