// Package center — клиент центра: пульт ходит в тот же API, что и
// панель оператора.
//
// Отдельного протокола для пульта нет намеренно. Кнопка на пульте и
// кнопка в панели должны приводить к одному и тому же, а два пути с
// разными проверками рано или поздно разойдутся в поведении.
package center

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"
)

// Client — сессия пульта в центре.
type Client struct {
	base  string
	login string
	pass  string
	http  *http.Client
	log   *slog.Logger

	mu       sync.Mutex
	loggedIn bool
}

// Unit — состояние узла, как его видит центр.
type Unit struct {
	Unit string `json:"unit"`
	Name string `json:"name"`
	// On — подтверждённое состояние: сборщик отчитался, что реле
	// щёлкнуло. Лампа кнопки горит именно по нему.
	On bool `json:"on"`
	// Wait — команда ушла, ответа сборщика ещё нет.
	Wait bool `json:"wait"`
	Want bool `json:"want"`
}

// Room — помещение со списком узлов.
type Room struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Equipment []Unit `json:"equipment"`
}

// Snapshot — состояние объекта целиком.
type Snapshot struct {
	Rooms []Room
	// Alarm — объект в аварийной остановке. Считается по состоянию
	// помещений: авария общая, и достаточно одного помещения.
	Alarm bool
}

func New(base, login, pass string, timeout time.Duration, log *slog.Logger) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		base:  base,
		login: login,
		pass:  pass,
		http:  &http.Client{Jar: jar, Timeout: timeout},
		log:   log,
	}
}

// State читает состояние всех помещений.
//
// Центр отдаёт комнаты массивом верхнего уровня, а не объектом с полем
// rooms: панель получает их тем же запросом, и оборачивать список ради
// одного поля там не стали.
func (c *Client) State(ctx context.Context) (Snapshot, error) {
	var rooms []Room
	if err := c.do(ctx, http.MethodGet, "/api/v1/rooms", nil, &rooms); err != nil {
		return Snapshot{}, err
	}

	snap := Snapshot{Rooms: rooms}
	for _, r := range rooms {
		if r.State == "alarm" {
			snap.Alarm = true
			break
		}
	}
	return snap, nil
}

// SetUnit переключает узел помещения.
func (c *Client) SetUnit(ctx context.Context, room, unit string, on bool) error {
	req := map[string]any{"unit": unit, "on": on}
	return c.do(ctx, http.MethodPost, "/api/v1/rooms/"+room+"/equipment", req, nil)
}

// Alarm объявляет аварийную остановку по всему объекту.
func (c *Client) Alarm(ctx context.Context, reason string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/alarm", map[string]any{"reason": reason}, nil)
}

// do выполняет запрос, при необходимости войдя заново.
//
// Сессия живёт в cookie и может истечь или пропасть при перезапуске
// центра. Для пульта это не повод звать человека: один повторный вход
// и повтор запроса.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if err := c.ensureLogin(ctx); err != nil {
		return err
	}

	code, err := c.call(ctx, method, path, in, out)
	if err != nil {
		return err
	}
	if code == http.StatusUnauthorized {
		c.mu.Lock()
		c.loggedIn = false
		c.mu.Unlock()
		if err := c.ensureLogin(ctx); err != nil {
			return err
		}
		code, err = c.call(ctx, method, path, in, out)
		if err != nil {
			return err
		}
	}
	if code >= 300 {
		return fmt.Errorf("центр ответил %d на %s", code, path)
	}
	return nil
}

func (c *Client) ensureLogin(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loggedIn {
		return nil
	}

	code, err := c.call(ctx, http.MethodPost, "/api/v1/login",
		map[string]any{"login": c.login, "password": c.pass}, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("центр не принял учётные данные пульта: %d", code)
	}
	c.loggedIn = true
	c.log.Info("пульт вошёл в центр", "login", c.login)
	return nil
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) (int, error) {
	var body *bytes.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("центр недоступен: %w", err)
	}
	defer resp.Body.Close()

	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("ответ центра не разобран: %w", err)
		}
	}
	return resp.StatusCode, nil
}
