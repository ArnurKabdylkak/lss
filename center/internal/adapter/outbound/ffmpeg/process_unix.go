//go:build !windows

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// Управление процессом ffmpeg: часть, зависящая от системы.
//
// ffmpeg порождает потомков — читателей RTSP, — и убивать нужно всю
// группу, иначе они остаются висеть и держат камеру. На Unix это
// делается группой процессов, на Windows групп нет, и там свой путь.

// setGroup помещает процесс в собственную группу.
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// stopGroup завершает группу процессов сигналом.
//
// gentle — дать ffmpeg дописать файл: по SIGINT он закрывает контейнер
// и выходит, а убитый оставил бы ролик без хвоста.
func stopGroup(cmd *exec.Cmd, gentle bool) error {
	sig := syscall.SIGTERM
	if gentle {
		sig = syscall.SIGINT
	}
	return syscall.Kill(-cmd.Process.Pid, sig)
}
