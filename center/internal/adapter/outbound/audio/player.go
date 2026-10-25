// Package audio — звук тренировки: фоновые эффекты и оповещения.
//
// Звук живёт в центре, а не на сборщиках. У центра акустика зала и
// записанные фразы; сборщик о звуковых файлах не знает вовсе — он
// щёлкает реле, и тянуть к нему ещё и звуковую дорожку значило бы
// держать записи в трёх местах и следить, чтобы они не разошлись.
//
// Проигрывает внешняя программа, а не библиотека: ffmpeg в образе уже
// есть ради видеотракта и умеет отдавать звук в ALSA. Своего декодера
// ради двух дорожек заводить незачем.
package audio

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultPlayer — чем проигрывать по умолчанию. %s заменяется путём
// к файлу, %loop% — флагом бесконечного повтора.
var DefaultPlayer = []string{
	"ffmpeg", "-hide_banner", "-loglevel", "error",
	"%loop%", "-re", "-i", "%s", "-f", "alsa", "default",
}

// Player ведёт звук объекта.
type Player struct {
	dir string
	cmd []string
	log *slog.Logger

	mu sync.Mutex
	// loops — что сейчас крутится, по ключу дорожки. Ключ, а не имя
	// файла: одна и та же запись может звучать в двух помещениях, и
	// остановить нужно именно нужное.
	loops map[string]*exec.Cmd
	// once — что играет разово, по пути к файлу. Нужно, чтобы одна
	// запись не наложилась сама на себя.
	once map[string]*exec.Cmd
}

// New собирает проигрыватель. Пустой каталог означает, что звука на
// объекте нет: вызовы тогда молча ничего не делают.
func New(dir string, player []string, log *slog.Logger) *Player {
	if len(player) == 0 {
		player = DefaultPlayer
	}
	return &Player{
		dir: dir, cmd: player, log: log,
		loops: map[string]*exec.Cmd{},
		once:  map[string]*exec.Cmd{},
	}
}

// Enabled сообщает, настроен ли звук.
func (p *Player) Enabled() bool { return p != nil && p.dir != "" }

// Once проигрывает запись один раз: так звучат оповещения.
//
// Одна и та же запись дважды разом не звучит. Аварию объявляют сразу
// по всем помещениям, и если зал у них общий, фраза наложилась бы
// сама на себя втрое и превратилась в шум. Там, где у помещения своя
// запись, файлы разные — и звучат они каждый свой.
func (p *Player) Once(room, name string) error {
	if !p.Enabled() {
		return nil
	}
	file, err := p.file(room, name)
	if err != nil {
		return err
	}

	p.mu.Lock()
	if _, playing := p.once[file]; playing {
		p.mu.Unlock()
		return nil
	}

	cmd, err := p.start(file, false)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	p.once[file] = cmd
	p.mu.Unlock()

	go func() {
		if err := cmd.Wait(); err != nil {
			p.log.Warn("запись не доиграна", "звук", name, "err", err)
		}
		p.mu.Lock()
		if p.once[file] == cmd {
			delete(p.once, file)
		}
		p.mu.Unlock()
	}()

	p.log.Info("звук", "запись", name, "файл", file, "помещение", room)
	return nil
}

// Loop крутит запись по кругу, пока её не остановят. Так звучат
// фоновые эффекты задымлённого зала.
func (p *Player) Loop(room, name string) error {
	if !p.Enabled() {
		return nil
	}
	file, err := p.file(room, name)
	if err != nil {
		return err
	}

	key := room + "/" + name

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, playing := p.loops[key]; playing {
		// Уже звучит: второй экземпляр наложился бы сам на себя.
		return nil
	}

	cmd, err := p.start(file, true)
	if err != nil {
		return err
	}
	p.loops[key] = cmd

	go func() {
		// Завершение ждём обязательно, иначе процесс остаётся
		// зомби: ffmpeg живёт столько же, сколько тренировка.
		_ = cmd.Wait()
		p.mu.Lock()
		if p.loops[key] == cmd {
			delete(p.loops, key)
		}
		p.mu.Unlock()
	}()

	p.log.Info("звук пущен по кругу", "запись", name, "помещение", room)
	return nil
}

// Stop останавливает дорожку помещения.
func (p *Player) Stop(room, name string) {
	if !p.Enabled() {
		return
	}
	key := room + "/" + name

	p.mu.Lock()
	cmd, playing := p.loops[key]
	delete(p.loops, key)
	p.mu.Unlock()

	if !playing || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil {
		p.log.Warn("звук не остановлен", "запись", name, "помещение", room, "err", err)
		return
	}
	p.log.Info("звук остановлен", "запись", name, "помещение", room)
}

// StopAll глушит всё. Вызывается при остановке центра: оставленная
// играть запись в пустом зале — это тревога на ровном месте.
func (p *Player) StopAll() {
	if !p.Enabled() {
		return
	}
	p.mu.Lock()
	running := make([]*exec.Cmd, 0, len(p.loops)+len(p.once))
	for key, cmd := range p.loops {
		running = append(running, cmd)
		delete(p.loops, key)
	}
	for key, cmd := range p.once {
		running = append(running, cmd)
		delete(p.once, key)
	}
	p.mu.Unlock()

	for _, cmd := range running {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

// start запускает проигрыватель.
func (p *Player) start(file string, loop bool) (*exec.Cmd, error) {
	args := make([]string, 0, len(p.cmd))
	for _, a := range p.cmd[1:] {
		switch a {
		case "%loop%":
			// Флаг повтора вставляется только для фоновых дорожек:
			// оповещение, зациклившись, не даст работать залу.
			if loop {
				args = append(args, "-stream_loop", "-1")
			}
		default:
			args = append(args, strings.ReplaceAll(a, "%s", file))
		}
	}

	cmd := exec.Command(p.cmd[0], args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("запуск проигрывателя: %w", err)
	}
	return cmd, nil
}

// Форматы записей. Проигрывает ffmpeg, и ограничивать его одним
// форматом незачем: студия отдаёт то, что отдаёт.
var formats = []string{".wav", ".mp3", ".ogg", ".flac", ".m4a", ".opus"}

// file ищет запись: сначала свою для помещения, потом общую.
//
// Своя нужна там, где залы звучат по-разному, а общая — чтобы не
// копировать одно и то же оповещение по разу на комнату.
func (p *Player) file(room, name string) (string, error) {
	names := []string{name}
	if room != "" {
		names = append([]string{room + "-" + name}, names...)
	}

	for _, n := range names {
		for _, ext := range formats {
			path := filepath.Join(p.dir, n+ext)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("запись %q не найдена в %s (%s)",
		name, p.dir, strings.Join(formats, ", "))
}
