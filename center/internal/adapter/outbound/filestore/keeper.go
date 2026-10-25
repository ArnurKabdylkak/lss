// Package filestore хранит состояние центра файлом рядом с программой.
//
// Нужен настольной сборке для Windows: там центр ставят одним файлом,
// и поднимать рядом отдельную базу неуместно — оператор включает
// программу, а не администрирует сервер. На сервере с MongoDB этот
// путь не используется, там хранилище настоящее.
//
// Что хранится: открытые и закрытые занятия, картотека, состояния
// комнат и журнал аварий. Телеметрия сюда не пишется намеренно — её
// поток за смену измеряется сотнями тысяч пакетов, и файл превратился
// бы в помойку. Показания живут в памяти, а то, ради чего их
// собирали, уходит в отчёт занятия.
package filestore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/repo"
)

// SaveInterval — как часто снимок уходит на диск.
//
// Не на каждое изменение: занятие меняется по нескольку раз в
// секунду, и запись файла на каждое движение счётчика ничего не
// добавляет к надёжности, зато изнашивает диск. Пяти секунд хватает:
// столько данных не жалко при внезапном пропадании питания, а
// закрытое занятие сохраняется сразу — его закрытие вызывает Save.
const SaveInterval = 5 * time.Second

// Keeper связывает хранилища в памяти с файлом на диске.
type Keeper struct {
	path     string
	sessions *repo.Sessions
	cards    *repo.Cards
	states   *repo.States
	alarms   *repo.Alarms
	log      *slog.Logger
}

// New собирает хранителя. Любая из частей может быть nil — тогда она
// не сохраняется и не восстанавливается.
func New(path string, sessions *repo.Sessions, cards *repo.Cards,
	states *repo.States, alarms *repo.Alarms, log *slog.Logger) *Keeper {

	return &Keeper{
		path: path, sessions: sessions, cards: cards,
		states: states, alarms: alarms, log: log,
	}
}

// Load читает снимок с диска. Отсутствие файла — не ошибка: так
// выглядит первый запуск.
func (k *Keeper) Load() error {
	raw, err := os.ReadFile(k.path)
	if os.IsNotExist(err) {
		k.log.Info("хранилище пустое, начинаем с чистого листа", "файл", k.path)
		return nil
	}
	if err != nil {
		return fmt.Errorf("чтение хранилища: %w", err)
	}

	snap, err := repo.DecodeSnapshot(raw)
	if err != nil {
		// Испорченный файл не должен мешать работать: смена важнее
		// прошлых записей. Но и затирать его молча нельзя — он
		// отодвигается, чтобы с ним можно было разобраться.
		broken := k.path + ".broken"
		if renameErr := os.Rename(k.path, broken); renameErr == nil {
			k.log.Error("хранилище испорчено, отложено",
				"файл", broken, "err", err)
		} else {
			k.log.Error("хранилище испорчено", "err", err)
		}
		return nil
	}

	if k.sessions != nil {
		k.sessions.Restore(snap.Sessions)
	}
	if k.cards != nil {
		k.cards.Restore(snap.Cards)
	}
	if k.states != nil {
		k.states.Restore(snap.States)
	}
	if k.alarms != nil {
		k.alarms.Restore(snap.Alarms)
	}

	k.log.Info("хранилище прочитано", "файл", k.path,
		"карточек", len(snap.Cards), "комнат", len(snap.States),
		"аварий", len(snap.Alarms))
	return nil
}

// Save записывает снимок на диск.
//
// Пишется во временный файл и переименовывается: обрыв питания
// посреди записи иначе оставил бы обрезанный файл, и потерялось бы
// всё, а не последние пять секунд.
func (k *Keeper) Save() error {
	var snap repo.Snapshot
	if k.sessions != nil {
		snap.Sessions = k.sessions.Snapshot()
	}
	if k.cards != nil {
		snap.Cards = k.cards.Snapshot()
	}
	if k.states != nil {
		snap.States = k.states.Snapshot()
	}
	if k.alarms != nil {
		snap.Alarms = k.alarms.Snapshot()
	}

	raw, err := snap.Encode()
	if err != nil {
		return err
	}

	if dir := filepath.Dir(k.path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("каталог хранилища: %w", err)
		}
	}

	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return fmt.Errorf("запись хранилища: %w", err)
	}
	if err := os.Rename(tmp, k.path); err != nil {
		return fmt.Errorf("замена хранилища: %w", err)
	}
	return nil
}

// Run сохраняет снимок с заданным шагом и ещё раз при остановке.
func (k *Keeper) Run(ctx context.Context) {
	t := time.NewTicker(SaveInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			// Последний снимок — самый важный: в нём итог смены.
			if err := k.Save(); err != nil {
				k.log.Error("хранилище не сохранено при остановке", "err", err)
			}
			return
		case <-t.C:
			if err := k.Save(); err != nil {
				k.log.Error("хранилище не сохранено", "err", err)
			}
		}
	}
}
