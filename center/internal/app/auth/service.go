// Package auth — вход оператора тренажёра по логину и паролю.
//
// Тренажёром управляют люди, и журнал действий (§7.7) имеет смысл
// только тогда, когда видно, кто именно отдал команду. Токена в
// конфигурации для этого мало: он общий и обезличенный.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role — что оператору позволено.
type Role string

const (
	RoleOperator Role = "operator" // наблюдение и управление
	RoleViewer   Role = "viewer"   // только наблюдение
)

var (
	ErrBadCredentials = errors.New("неверный логин или пароль")
	ErrNoSession      = errors.New("нужно войти")
	ErrForbidden      = errors.New("нет прав на это действие")
)

// SessionTTL — сколько живёт сессия без обращений.
//
// Смена тренажёра длится часы, поэтому срок щедрый; но бесконечная
// сессия на общем пульте означала бы, что журнал врёт о том, кто
// сейчас за пультом.
const SessionTTL = 12 * time.Hour

// User — запись из файла пользователей.
type User struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Role  Role   `json:"role"`
	// Хеш пароля bcrypt. Пароли в открытом виде не хранятся нигде:
	// файл лежит на узле в закрытом контуре, но это не повод.
	Hash string `json:"hash"`
}

// Session — вошедший оператор.
type Session struct {
	Token   string    `json:"-"`
	Login   string    `json:"login"`
	Name    string    `json:"name"`
	Role    Role      `json:"role"`
	Expires time.Time `json:"expires"`
}

// CanOperate — вправе ли отдавать команды.
func (s Session) CanOperate() bool { return s.Role == RoleOperator }

// Store — постоянное хранилище сессий.
//
// Без него смена разлогинивается при каждом перезапуске центра, а
// перезапуск случается и от обновления, и от перебоя питания.
// Интерфейс объявлен здесь: прикладной слой диктует, что ему нужно.
type Store interface {
	Save(ctx context.Context, s Session) error
	Delete(ctx context.Context, token string) error
	// All возвращает непросроченные сессии для восстановления при старте.
	All(ctx context.Context) ([]Session, error)
}

// storeTimeout — предел ожидания хранилища. База не должна задерживать
// вход: сессия уже есть в памяти, запись в базу — подстраховка.
const storeTimeout = 3 * time.Second

// refreshAfter — как часто продление сессии доходит до хранилища.
//
// Продлевать при каждом запросе значило бы писать в базу на каждый
// опрос панели — это десятки записей в минуту ради поля, точность
// которого никому не нужна.
const refreshAfter = 15 * time.Minute

// Service хранит пользователей и выданные сессии.
type Service struct {
	mu       sync.RWMutex
	users    map[string]User
	sessions map[string]*Session

	store Store
	log   *slog.Logger
	// saved — когда срок сессии последний раз доезжал до хранилища.
	saved map[string]time.Time
}

// New создаёт службу с заданными пользователями.
func New(users []User) (*Service, error) {
	s := &Service{
		users:    make(map[string]User, len(users)),
		sessions: make(map[string]*Session),
		saved:    make(map[string]time.Time),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	for _, u := range users {
		login := strings.ToLower(strings.TrimSpace(u.Login))
		if login == "" || u.Hash == "" {
			return nil, fmt.Errorf("пользователь без логина или хеша пароля")
		}
		if u.Role != RoleOperator && u.Role != RoleViewer {
			return nil, fmt.Errorf("пользователь %q: неизвестная роль %q", login, u.Role)
		}
		u.Login = login
		s.users[login] = u
	}
	return s, nil
}

// LoadFile читает файл пользователей.
func LoadFile(path string) (*Service, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение файла пользователей: %w", err)
	}

	var users []User
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("разбор файла пользователей: %w", err)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("в файле пользователей никого нет")
	}
	return New(users)
}

// WithStore подключает постоянное хранилище и поднимает сессии,
// выданные до перезапуска.
//
// Сессии остаются и в памяти: их читают на каждом запросе, и ходить
// за этим в базу незачем. Хранилище — зеркало, а не источник.
func (s *Service) WithStore(ctx context.Context, store Store, log *slog.Logger) error {
	if store == nil {
		return nil
	}

	saved, err := store.All(ctx)
	if err != nil {
		return fmt.Errorf("восстановление сессий: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.store = store
	if log != nil {
		s.log = log
	}

	var restored int
	now := time.Now()
	for _, sess := range saved {
		// Пользователя могли удалить из файла, пока центр не работал.
		if _, ok := s.users[sess.Login]; !ok {
			continue
		}
		if now.After(sess.Expires) {
			continue
		}
		copy := sess
		s.sessions[sess.Token] = &copy
		s.saved[sess.Token] = now
		restored++
	}

	s.log.Info("сессии операторов восстановлены", "сессий", restored)
	return nil
}

// persist сохраняет сессию в хранилище, не задерживая вызывающего
// дольше storeTimeout. Вызывается без мьютекса.
func (s *Service) persist(sess Session) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()

	if err := s.store.Save(ctx, sess); err != nil {
		// Вход уже состоялся: сессия в памяти. Молчать нельзя —
		// после перезапуска она не вернётся.
		s.log.Error("сессия не сохранена", "login", sess.Login, "err", err)
	}
}

func (s *Service) forget(token string) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()

	if err := s.store.Delete(ctx, token); err != nil {
		s.log.Error("сессия не удалена из хранилища", "err", err)
	}
}

// Login проверяет пароль и выдаёт сессию.
//
// Запись в хранилище идёт после того, как мьютекс отпущен: база не
// должна держать замок, под которым работает весь вход.
func (s *Service) Login(login, password string) (Session, error) {
	sess, err := s.issue(login, password)
	if err != nil {
		return Session{}, err
	}
	s.persist(sess)
	return sess, nil
}

func (s *Service) issue(login, password string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[strings.ToLower(strings.TrimSpace(login))]
	if !ok {
		// Пароль всё равно проверяется по фиктивному хешу: иначе по
		// времени ответа видно, существует логин или нет.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return Session{}, ErrBadCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)); err != nil {
		return Session{}, ErrBadCredentials
	}

	token := newToken()
	sess := &Session{
		Token:   token,
		Login:   u.Login,
		Name:    u.Name,
		Role:    u.Role,
		Expires: time.Now().Add(SessionTTL),
	}
	s.sessions[token] = sess
	s.saved[token] = time.Now()
	s.cleanup()

	return *sess, nil
}

// Session возвращает сессию по токену и продлевает её.
func (s *Service) Session(token string) (Session, error) {
	sess, refreshed, err := s.touch(token)
	if err != nil {
		return Session{}, err
	}
	// В хранилище продление доезжает не каждый раз: см. refreshAfter.
	if refreshed {
		s.persist(sess)
	}
	return sess, nil
}

func (s *Service) touch(token string) (Session, bool, error) {
	if token == "" {
		return Session{}, false, ErrNoSession
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, false, ErrNoSession
	}
	if time.Now().After(sess.Expires) {
		delete(s.sessions, token)
		delete(s.saved, token)
		return Session{}, false, ErrNoSession
	}

	// Пока оператор работает, сессия не истекает у него под руками.
	sess.Expires = time.Now().Add(SessionTTL)

	refresh := time.Since(s.saved[token]) > refreshAfter
	if refresh {
		s.saved[token] = time.Now()
	}
	return *sess, refresh, nil
}

// Logout завершает сессию.
func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	delete(s.saved, token)
	s.mu.Unlock()

	s.forget(token)
}

// cleanup убирает просроченные сессии. Вызывается под мьютексом.
func (s *Service) cleanup() {
	now := time.Now()
	for token, sess := range s.sessions {
		if now.After(sess.Expires) {
			delete(s.sessions, token)
		}
	}
}

// HashPassword считает хеш для файла пользователей.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < 8 {
		return "", fmt.Errorf("пароль короче 8 символов")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func newToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// dummyHash — хеш заведомо неподходящего пароля; нужен, чтобы проверка
// несуществующего логина занимала столько же времени, сколько обычная.
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// SameToken сравнивает токены за постоянное время.
func SameToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
