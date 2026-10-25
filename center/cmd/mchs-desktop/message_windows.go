//go:build windows

package main

import (
	"log/slog"
	"syscall"
	"unsafe"
)

// Сообщение об ошибке окном, а не строкой в консоли.
//
// У оконного приложения консоли нет: оператор, запустивший его с
// рабочего стола, не увидел бы ни строчки и решил бы, что приложение
// «просто не открывается». Поэтому причина показывается окном и
// дублируется в журнал рядом с программой.
func fail(log *slog.Logger, what string, err error) {
	log.Error(what, "err", err)

	text := what
	if err != nil {
		text += "\n\n" + err.Error()
	}
	text += "\n\nПодробности — в файле mchs-center.log рядом с программой."

	messageBox(text, "Тренажёрный комплекс")
}

func messageBox(text, title string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")

	t, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	c, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}

	// MB_OK | MB_ICONERROR
	const flags = 0x00000000 | 0x00000010
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)), flags)
}
