//go:build windows

package main

import (
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/ArnurKabdylkak/lss/center/internal/adapter/outbound/deps"
)

// Компонент просмотра веб-страниц (WebView2).
//
// Окно панели рисует он. На Windows 11 и свежих сборках Windows 10 он
// уже стоит в системе, на старых — нет, и обращение к нему роняет
// программу без объяснений. Поэтому наличие проверяется заранее, а
// установщик лежит внутри нашего файла.

// Ключ, по которому система отмечает установленный компонент. Один и
// тот же у Microsoft во всех сборках; версия лежит в значении pv.
const webviewClient = `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// webviewVersion возвращает установленную версию компонента.
func webviewVersion() (string, bool) {
	// Смотрим в трёх местах: для всех пользователей, для текущего и
	// в ветке 32-разрядных программ — Microsoft пишет по-разному в
	// зависимости от того, как компонент попал в систему.
	places := []struct {
		key  registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + webviewClient[len(`SOFTWARE\`):]},
		{registry.LOCAL_MACHINE, webviewClient},
		{registry.CURRENT_USER, webviewClient},
	}

	for _, p := range places {
		k, err := registry.OpenKey(p.key, p.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && pv != "" && pv != "0.0.0.0" {
			return pv, true
		}
	}
	return "", false
}

// ensureWebView проверяет компонент и при нужде ставит вложенный.
//
// Установщик тянет файлы из сети. В закрытом контуре её нет, и тогда
// остаётся сказать об этом человеку: молча падать нельзя, а ставить
// компонент за него мы не можем.
func ensureWebView(log *slog.Logger) error {
	if v, ok := webviewVersion(); ok {
		log.Info("компонент просмотра найден", "версия", v)
		return nil
	}

	file, ok := deps.WebViewSetup()
	if !ok {
		return fmt.Errorf("компонента WebView2 нет в системе, а установщик в сборку не вложен")
	}

	log.Info("компонента просмотра нет, ставлю вложенный")

	u := deps.New(filepath.Join(exeDir(), "bin"), log)
	path, err := u.Ensure(file)
	if err != nil {
		return err
	}

	// Тихая установка для всех пользователей: оператору нечего
	// отвечать в чужих окнах.
	cmd := exec.Command(path, "/silent", "/install")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("установка компонента WebView2: %w "+
			"(в закрытом контуре поставьте его заранее: файл bin\\webview2-setup.exe "+
			"требует связи с сетью Microsoft)", err)
	}

	// Система отмечает установку не мгновенно.
	for range 10 {
		if v, ok := webviewVersion(); ok {
			log.Info("компонент просмотра установлен", "версия", v)
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("компонент WebView2 не появился после установки")
}
