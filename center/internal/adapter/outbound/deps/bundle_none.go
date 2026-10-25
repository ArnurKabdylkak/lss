//go:build !withdeps

package deps

// Без тега withdeps зависимости не вложены: в контейнере и на Linux
// ffmpeg ставится пакетом системы, и таскать его копию внутри
// исполняемого файла незачем.
func FFmpeg() (File, bool) { return File{}, false }

// WebViewSetup — вне сборки с вложениями установщика нет.
func WebViewSetup() (File, bool) { return File{}, false }
