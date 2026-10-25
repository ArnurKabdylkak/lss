// Package direct — аварийная остановка в обход центра.
//
// Обычные кнопки пульта работают только через центр: сценарий,
// журнал и состояние живут там. Но остановка не должна зависеть от
// одного узла. Если центр не ответил, пульт стучится прямо в сборщики
// помещений — это запасной путь, и ходит по нему только авария.
package direct

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Collectors — сборщики помещений, известные пульту по адресам.
type Collectors struct {
	urls  []string
	token string
	http  *http.Client
	log   *slog.Logger
}

func New(urls []string, token string, timeout time.Duration, log *slog.Logger) *Collectors {
	return &Collectors{
		urls:  urls,
		token: token,
		http:  &http.Client{Timeout: timeout},
		log:   log,
	}
}

// Enabled сообщает, настроен ли запасной путь.
func (c *Collectors) Enabled() bool { return c != nil && len(c.urls) > 0 }

// Alarm рассылает аварию всем сборщикам сразу.
//
// Параллельно и без остановки на первой ошибке: недоступность одного
// помещения не повод не останавливать остальные.
func (c *Collectors) Alarm(ctx context.Context, reason string) error {
	if !c.Enabled() {
		return fmt.Errorf("прямой путь к сборщикам не настроен")
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed []string
	)
	for _, url := range c.urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := c.one(ctx, url, reason); err != nil {
				c.log.Error("сборщик не принял аварию напрямую", "сборщик", url, "err", err)
				mu.Lock()
				failed = append(failed, url)
				mu.Unlock()
				return
			}
			c.log.Warn("АВАРИЯ отправлена сборщику напрямую, минуя центр", "сборщик", url)
		}(url)
	}
	wg.Wait()

	if len(failed) > 0 {
		return fmt.Errorf("не приняли аварию: %s", strings.Join(failed, ", "))
	}
	return nil
}

func (c *Collectors) one(ctx context.Context, url, reason string) error {
	body := bytes.NewReader([]byte(`{"reason":` + quote(reason) + `}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/alarm", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("ответ %d", resp.StatusCode)
	}
	return nil
}

// quote экранирует причину для JSON без тяжёлой сериализации: тело
// запроса состоит из одного поля, а путь аварийный.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\r", " ", "\t", " ")
	return `"` + r.Replace(s) + `"`
}
