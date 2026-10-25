package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
)

// Вход оператора тренажёра и разграничение прав.
//
// Наблюдатель видит состояние, оператор ещё и управляет пушкой и
// препятствиями. Разделение обязательно: журнал действий бессмыслен,
// если все ходят под одним доступом.

type guard struct {
	auth *auth.Service
}

// enabled: пока пользователи не заданы, проверка выключена — иначе на
// отладке внутри контура пришлось бы заводить учётные записи.
func (g guard) enabled() bool { return g.auth != nil }

// viewer пропускает любого вошедшего.
func (g guard) viewer(next sessionHandler) http.Handler {
	return g.wrap(next, false)
}

// viewerHandler пропускает вошедшего к обычному обработчику —
// тому, которому сессия не нужна (раздача файлов трансляций).
func (g guard) viewerHandler(next http.Handler) http.Handler {
	return g.wrap(func(w http.ResponseWriter, r *http.Request, _ auth.Session) {
		next.ServeHTTP(w, r)
	}, false)
}

// operator пропускает только тех, кому позволено управлять.
func (g guard) operator(next sessionHandler) http.Handler {
	return g.wrap(next, true)
}

// sessionHandler — обработчик, знающий, кто его вызвал.
type sessionHandler func(w http.ResponseWriter, r *http.Request, s auth.Session)

func (g guard) wrap(next sessionHandler, needOperate bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.enabled() {
			// Без учётных записей команды подписываются именем из тела
			// запроса — так было до появления входа по паролю.
			next(w, r, auth.Session{Role: auth.RoleOperator})
			return
		}

		sess, err := g.auth.Session(tokenOf(r))
		if err != nil {
			// 401, а не 403: панель по этому коду показывает вход,
			// а не сообщение «нет прав».
			fail(w, http.StatusUnauthorized, auth.ErrNoSession.Error())
			return
		}
		if needOperate && !sess.CanOperate() {
			fail(w, http.StatusForbidden, auth.ErrForbidden.Error())
			return
		}

		next(w, r, sess)
	})
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token   string    `json:"token"`
	Login   string    `json:"login"`
	Name    string    `json:"name"`
	Role    auth.Role `json:"role"`
	Expires string    `json:"expires"`
}

func (g guard) login(w http.ResponseWriter, r *http.Request) {
	if !g.enabled() {
		fail(w, http.StatusNotImplemented, "вход не настроен: учётные записи не заданы")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "тело запроса не разобрано")
		return
	}

	sess, err := g.auth.Login(req.Login, req.Password)
	if err != nil {
		// Не уточняем, логин неверен или пароль: подсказка помогла бы
		// подбирать учётные записи.
		fail(w, http.StatusUnauthorized, auth.ErrBadCredentials.Error())
		return
	}

	setSessionCookie(w, sess)

	writeJSON(w, http.StatusOK, loginResponse{
		Token:   sess.Token,
		Login:   sess.Login,
		Name:    sess.Name,
		Role:    sess.Role,
		Expires: sess.Expires.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// sessionCookie — имя cookie с токеном сессии.
//
// Токена в заголовке мало: видео запрашивает плеер, а не наш код.
// Имена сегментов в плейлисте относительные, и строка запроса с
// токеном до них не доходит — поэтому плейлист грузился, а сегменты
// получали 401. Cookie браузер подставляет сам в каждый запрос того
// же источника, включая сегменты.
const sessionCookie = "mchs_session"

func setSessionCookie(w http.ResponseWriter, sess auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: sess.Token,
		Path:  "/",
		// HttpOnly: токен не нужен коду панели — она хранит свою копию
		// и шлёт её заголовком; из JavaScript эту трогать незачем.
		HttpOnly: true,
		// Lax, а не Strict: панель открывают по ссылке из вкладки
		// с документацией, и вход не должен при этом слетать.
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.Expires,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (g guard) logout(w http.ResponseWriter, r *http.Request) {
	if g.enabled() {
		g.auth.Logout(tokenOf(r))
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// me сообщает панели, кто вошёл: после перезагрузки страницы токен
// есть, а имя и роль надо получить заново.
func (g guard) me(w http.ResponseWriter, r *http.Request) {
	if !g.enabled() {
		writeJSON(w, http.StatusOK, loginResponse{Role: auth.RoleOperator, Name: "без учётных записей"})
		return
	}

	sess, err := g.auth.Session(tokenOf(r))
	if err != nil {
		fail(w, http.StatusUnauthorized, auth.ErrNoSession.Error())
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Login:   sess.Login,
		Name:    sess.Name,
		Role:    sess.Role,
		Expires: sess.Expires.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func tokenOf(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if t := r.Header.Get("X-Token"); t != "" {
		return t
	}
	// Cookie — для запросов, которые делает не панель, а браузер:
	// сегменты видео и плейлисты (см. sessionCookie).
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	// Поток событий открывается через EventSource, который не умеет
	// задавать заголовки, поэтому токен принимается и в адресе.
	// Он попадает в журналы прокси — внутри закрытого контура это
	// приемлемо, наружу такую панель выставлять нельзя.
	return r.URL.Query().Get("token")
}

// operatorName возвращает имя для журнала действий.
//
// Имя берётся из сессии, а не из тела запроса: то, кто отдал команду,
// не должно зависеть от того, что клиент о себе написал.
func operatorName(s auth.Session, fallback string) string {
	if s.Login != "" {
		if s.Name != "" {
			return s.Name + " (" + s.Login + ")"
		}
		return s.Login
	}
	if fallback != "" {
		return fallback
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

var _ = errors.Is // errors используется в сигнатурах auth
