package httpapi

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Раздача собранной панели тем же сервисом, что и API.
//
// Один адрес на всё: панель, API, поток событий и видео. Это не только
// удобство развёртывания — cookie сессии и запросы плеера работают без
// оговорок про разные источники, а TLS настраивается в одном месте.
// Отдельный веб-сервер рядом означал бы ещё один процесс на хосте,
// которого на Windows-машине может и не быть.

// panelAvailable сообщает, есть ли что раздавать: каталог на диске
// или панель, вшитая в исполняемый файл.
func panelAvailable(root string) bool {
	if root != "" {
		return true
	}
	_, ok := embeddedPanel()
	return ok
}

// panelFiles отдаёт файлы панели с запасным маршрутом на index.html.
//
// Панель разложена по страницам: каждый раздел — свой файл. Если
// запрошенного файла нет, отдаётся index.html — тогда опечатка в
// адресе приводит на обзор, а не на страницу ошибки сервера.
func panelFiles(root string) http.Handler {
	if root == "" {
		// Панель вшита в сам исполняемый файл: на Windows центр
		// ставят одним файлом, и отдельный каталог рядом с ним —
		// лишний повод его потерять или обновить наполовину.
		if embedded, ok := embeddedPanel(); ok {
			return panelFS(embedded)
		}
	}

	files := http.FileServer(http.Dir(root))
	index := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))

		// Выход за пределы каталога панели: такого пути у неё нет.
		if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}

		if info, err := os.Stat(filepath.Join(root, clean)); err == nil && !info.IsDir() {
			// Страницы панели кешировать нельзя: в них ссылки на файлы
			// сборки с хешами в именах. Закешированная страница тянет
			// старые файлы, и обновлённая панель не доезжает до
			// браузера — выглядит так, будто правок не было. Сами
			// файлы сборки неизменяемы и кешируются как обычно.
			if strings.HasSuffix(strings.ToLower(clean), ".html") {
				w.Header().Set("Cache-Control", "no-store")
			}
			files.ServeHTTP(w, r)
			return
		}

		// index.html кешировать нельзя: в нём ссылки на файлы сборки,
		// и после обновления панели браузер должен увидеть новые.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, index)
	})
}

// panelFS отдаёт вшитую панель. Правила те же, что и для каталога:
// страницы не кешируются, неизвестный путь ведёт на обзор.
func panelFS(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" {
			clean = "index.html"
		}

		if info, err := fs.Stat(root, clean); err == nil && !info.IsDir() {
			if strings.HasSuffix(strings.ToLower(clean), ".html") {
				w.Header().Set("Cache-Control", "no-store")
			}
			files.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		data, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
}
