package auth

import (
	"testing"
	"time"
)

func users(t *testing.T) *Service {
	t.Helper()

	operatorHash, err := HashPassword("оператор-пароль")
	if err != nil {
		t.Fatalf("хеш: %v", err)
	}
	viewerHash, _ := HashPassword("наблюдатель-пароль")

	s, err := New([]User{
		{Login: "petrov", Name: "Пётр Петров", Role: RoleOperator, Hash: operatorHash},
		{Login: "ivanov", Name: "Иван Иванов", Role: RoleViewer, Hash: viewerHash},
	})
	if err != nil {
		t.Fatalf("создание службы: %v", err)
	}
	return s
}

func TestВходПоПаролю(t *testing.T) {
	s := users(t)

	sess, err := s.Login("petrov", "оператор-пароль")
	if err != nil {
		t.Fatalf("вход отклонён: %v", err)
	}
	if sess.Token == "" {
		t.Error("сессия без токена")
	}
	if !sess.CanOperate() {
		t.Error("оператор не может управлять")
	}
}

func TestНеверныйПарольОтклоняется(t *testing.T) {
	s := users(t)

	if _, err := s.Login("petrov", "не тот пароль"); err != ErrBadCredentials {
		t.Errorf("ожидалось ErrBadCredentials, получено %v", err)
	}
	if _, err := s.Login("нет такого", "любой"); err != ErrBadCredentials {
		t.Errorf("несуществующий логин: %v", err)
	}
}

// Логин не должен зависеть от регистра: на пульте вводят как придётся.
func TestЛогинБезРегистра(t *testing.T) {
	s := users(t)

	if _, err := s.Login("  PETROV ", "оператор-пароль"); err != nil {
		t.Errorf("вход с другим регистром отклонён: %v", err)
	}
}

func TestНаблюдательНеУправляет(t *testing.T) {
	s := users(t)

	sess, err := s.Login("ivanov", "наблюдатель-пароль")
	if err != nil {
		t.Fatalf("вход отклонён: %v", err)
	}
	if sess.CanOperate() {
		t.Error("наблюдателю разрешено управление")
	}
}

func TestСессияПроверяетсяИЗавершается(t *testing.T) {
	s := users(t)
	sess, _ := s.Login("petrov", "оператор-пароль")

	got, err := s.Session(sess.Token)
	if err != nil {
		t.Fatalf("сессия не найдена: %v", err)
	}
	if got.Login != "petrov" {
		t.Errorf("не тот оператор: %q", got.Login)
	}

	s.Logout(sess.Token)
	if _, err := s.Session(sess.Token); err != ErrNoSession {
		t.Errorf("сессия жива после выхода: %v", err)
	}
	if _, err := s.Session("чужой токен"); err != ErrNoSession {
		t.Errorf("чужой токен принят: %v", err)
	}
}

func TestПросроченнаяСессияНеРаботает(t *testing.T) {
	s := users(t)
	sess, _ := s.Login("petrov", "оператор-пароль")

	// Состаривание вручную: ждать SessionTTL в тесте нельзя.
	s.mu.Lock()
	s.sessions[sess.Token].Expires = time.Now().Add(-time.Minute)
	s.mu.Unlock()

	if _, err := s.Session(sess.Token); err != ErrNoSession {
		t.Errorf("просроченная сессия принята: %v", err)
	}
}

// Пока оператор работает, сессия не должна истекать у него под руками.
func TestРаботаПродлеваетСессию(t *testing.T) {
	s := users(t)
	sess, _ := s.Login("petrov", "оператор-пароль")

	s.mu.Lock()
	s.sessions[sess.Token].Expires = time.Now().Add(time.Minute)
	s.mu.Unlock()

	got, err := s.Session(sess.Token)
	if err != nil {
		t.Fatalf("сессия не найдена: %v", err)
	}
	if time.Until(got.Expires) < SessionTTL-time.Minute {
		t.Error("сессия не продлена обращением")
	}
}

// Короткий пароль на общем пульте — приглашение его подобрать.
func TestКороткийПарольНеПринимается(t *testing.T) {
	if _, err := HashPassword("1234"); err == nil {
		t.Error("пароль из четырёх символов принят")
	}
	if _, err := HashPassword("длинный-пароль"); err != nil {
		t.Errorf("годный пароль отклонён: %v", err)
	}
}

func TestНеизвестнаяРольОтклоняется(t *testing.T) {
	hash, _ := HashPassword("какой-то-пароль")

	if _, err := New([]User{{Login: "x", Role: "начальник", Hash: hash}}); err == nil {
		t.Error("пользователь с неизвестной ролью принят")
	}
	if _, err := New([]User{{Login: "x", Role: RoleOperator}}); err == nil {
		t.Error("пользователь без хеша пароля принят")
	}
}
