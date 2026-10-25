//go:build embedpanel

package httpapi

import (
	"embed"
	"io/fs"
)

// Панель, вшитая в исполняемый файл.
//
// Собирается тегом embedpanel: в обычной сборке (Docker, Linux)
// панель лежит каталогом рядом и правится без пересборки центра. На
// Windows центр ставят одним файлом, и каталог рядом с ним — лишний
// повод потерять его или обновить наполовину.
//
// Содержимое кладёт скрипт сборки: scripts/build-windows.sh копирует
// сюда собранную панель перед go build.

//go:embed all:paneldist
var panelAssets embed.FS

func embeddedPanel() (fs.FS, bool) {
	sub, err := fs.Sub(panelAssets, "paneldist")
	if err != nil {
		return nil, false
	}
	// Пустой каталог означает, что панель забыли собрать: отдавать
	// пустоту хуже, чем честно работать без панели.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
