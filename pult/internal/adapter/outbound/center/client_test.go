package center

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Ответ центра на /api/v1/rooms — ровно в том виде, в каком его отдаёт
// панели настоящий центр: массив верхнего уровня, а не объект с полем
// rooms. Именно на этом пульт однажды молча «потерял связь»: разбор
// падал, а кнопки выглядели мёртвыми.
const roomsJSON = `[
  {"id":"room-1","name":"Теплокамера","state":"waiting",
   "equipment":[
     {"unit":"light","name":"Освещение","on":true},
     {"unit":"heat","name":"Тепловая пушка","on":false,"wait":true,"want":true}
   ]},
  {"id":"room-2","name":"Дымокамера","state":"alarm","equipment":[]}
]`

func TestСостояниеРазбираетсяИзМассива(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "x"})
		case "/api/v1/rooms":
			io.WriteString(w, roomsJSON)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "pult", "secret", time.Second, quiet())
	snap, err := c.State(context.Background())
	if err != nil {
		t.Fatalf("состояние не прочитано: %v", err)
	}

	if len(snap.Rooms) != 2 {
		t.Fatalf("комнат разобрано %d, ожидалось 2", len(snap.Rooms))
	}
	if !snap.Alarm {
		t.Error("авария по комнате room-2 не замечена")
	}

	units := snap.Rooms[0].Equipment
	if len(units) != 2 {
		t.Fatalf("узлов разобрано %d, ожидалось 2", len(units))
	}
	if !units[0].On {
		t.Error("подтверждённое состояние узла потеряно")
	}
	if !units[1].Wait || !units[1].Want {
		t.Errorf("ожидание подтверждения потеряно: %+v", units[1])
	}
}

// Сессия в центре может истечь или пропасть при его перезапуске. Для
// пульта это не повод звать человека: один повторный вход и повтор.
func TestСессияВосстанавливаетсяСама(t *testing.T) {
	var logins, rooms int
	authorized := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/login":
			logins++
			authorized = true
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "x"})
		case "/api/v1/rooms":
			rooms++
			if !authorized {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Центр перезапустился: первая попытка после входа
			// отвергается, дальше всё в порядке.
			if rooms == 1 {
				authorized = false
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, roomsJSON)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "pult", "secret", time.Second, quiet())
	if _, err := c.State(context.Background()); err != nil {
		t.Fatalf("состояние не прочитано после повторного входа: %v", err)
	}
	if logins != 2 {
		t.Errorf("входов в центр %d, ожидалось 2", logins)
	}
}

func TestПереключениеУзлаУходитВЦентр(t *testing.T) {
	var path, body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "x"})
			return
		}
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
	}))
	defer srv.Close()

	c := New(srv.URL, "pult", "secret", time.Second, quiet())
	if err := c.SetUnit(context.Background(), "room-2", "smoke", true); err != nil {
		t.Fatalf("команда не ушла: %v", err)
	}

	if path != "/api/v1/rooms/room-2/equipment" {
		t.Errorf("адрес команды: %s", path)
	}
	if !strings.Contains(body, `"unit":"smoke"`) || !strings.Contains(body, `"on":true`) {
		t.Errorf("тело команды: %s", body)
	}
}

// Отказ центра не должен выглядеть как успех: иначе пульт считает
// команду отправленной, а в зале ничего не произошло.
func TestОтказЦентраЭтоОшибка(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "x"})
			return
		}
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	c := New(srv.URL, "pult", "secret", time.Second, quiet())
	if err := c.SetUnit(context.Background(), "room-1", "light", true); err == nil {
		t.Error("отказ центра принят за успех")
	}
}
