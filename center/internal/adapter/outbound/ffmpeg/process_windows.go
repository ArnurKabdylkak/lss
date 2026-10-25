//go:build windows

package ffmpeg

import (
	"os/exec"
	"strconv"
)

// Управление процессом ffmpeg на Windows.
//
// Групп процессов, как в Unix, здесь нет, и сигналов тоже: SIGINT
// процессу чужой консоли не отправить. Потомков снимает taskkill с
// ключом дерева — иначе читатели RTSP остаются висеть и держат камеру.

// setGroup на Windows ничего не настраивает: группы задаются иначе,
// а дерево процессов снимается при остановке.
func setGroup(cmd *exec.Cmd) {}

// stopGroup снимает процесс вместе с потомками.
//
// gentle здесь ничего не меняет: мягкого сигнала, по которому ffmpeg
// дописал бы файл, на Windows нет. Ролик обрывается на последнем
// записанном кадре — контейнер MP4 это переживает, потому что пишется
// с фрагментацией (см. args в recorder.go).
func stopGroup(cmd *exec.Cmd, gentle bool) error {
	_ = gentle
	kill := exec.Command("taskkill", "/T", "/F", "/PID",
		strconv.Itoa(cmd.Process.Pid))
	return kill.Run()
}
