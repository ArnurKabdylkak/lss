//go:build withdeps

package deps

import _ "embed"

// Вложенные зависимости. Кладёт их скрипт сборки: держать в
// репозитории восемьдесят мегабайт чужого бинаря незачем, а каждая
// сборка всё равно берёт свежую проверенную версию.
//
// Собирается тегом withdeps: обычная сборка для Linux и контейнера
// остаётся лёгкой, там ffmpeg ставится пакетом системы.

//go:embed bundle/ffmpeg.exe.gz
var ffmpegGz []byte

//go:embed bundle/wv2setup.exe.gz
var webviewSetupGz []byte

// FFmpeg — вложенный ffmpeg для Windows, в сжатом виде.
func FFmpeg() (File, bool) {
	return File{Name: "ffmpeg.exe", Data: ffmpegGz, Executable: true}, true
}

// WebViewSetup — установщик компонента просмотра веб-страниц
// (WebView2). Нужен там, где его нет в системе: на Windows 11 и
// свежих Windows 10 он уже стоит, на старых сборках — нет.
func WebViewSetup() (File, bool) {
	return File{Name: "webview2-setup.exe", Data: webviewSetupGz, Executable: true}, true
}
