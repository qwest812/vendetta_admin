package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/auth"
)

// Предел считается по подсети и отпускает, когда старые попытки выходят
// из окна.
func TestSignupLimiter(t *testing.T) {
	l := newSignupLimiter()
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	const home = "203.0.113.0/24"

	for i := range signupAttempts {
		if !l.allow(home, start.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("попытка %d не пропущена, а предел %d", i+1, signupAttempts)
		}
	}
	if l.allow(home, start.Add(time.Hour)) {
		t.Error("попытка сверх предела пропущена")
	}
	if !l.allow("198.51.100.0/24", start.Add(time.Hour)) {
		t.Error("чужая подсеть попала под чужой предел")
	}

	// Первая попытка вышла из окна — одно место освободилось, но не больше.
	later := start.Add(signupWindow + 30*time.Second)
	if !l.allow(home, later) {
		t.Error("после выхода старой попытки из окна регистрация не пустила")
	}
	if l.allow(home, later) {
		t.Error("освободилось больше мест, чем вышло попыток")
	}
}

// Неудачи считаются и по подсети, и по логину: упереться можно в любой
// из пределов, а регистр почты счёт не делит.
func TestLoginLimiter(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	l := newLoginLimiter()
	const home = "203.0.113.0/24"
	for i := range loginSubnetFails {
		l.failed(home, fmt.Sprintf("user%d@x.y", i), now)
	}
	if !l.blocked(home, "other@x.y", now) {
		t.Error("подсеть перебрала предел, а вход с неё пускают")
	}
	if l.blocked("198.51.100.0/24", "other@x.y", now) {
		t.Error("чужая подсеть попала под чужой предел")
	}
	if l.blocked(home, "other@x.y", now.Add(loginSubnetWindow+time.Second)) {
		t.Error("окно подсети прошло, а вход не пускают")
	}

	l = newLoginLimiter()
	for i := range loginNameFails {
		l.failed(fmt.Sprintf("10.0.%d.0/24", i), "Root@X.y", now)
	}
	if !l.blocked("192.0.2.0/24", " root@x.y", now) {
		t.Error("логин перебирают с разных адресов, а вход в него пускают")
	}
	if l.blocked("192.0.2.0/24", "someone@x.y", now) {
		t.Error("предел одного логина задел другой")
	}
}

// Упёршийся в предел получает отказ до сверки пароля — и на форме сайта,
// и во входе расширения. У сервера здесь нет базы: дойди запрос до неё,
// тест упал бы.
func TestLoginRefusedOverLimit(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	s, err := NewServer(Deps{Log: log, Auth: auth.NewService(nil, nil, nil, log, time.Hour, false)})
	if err != nil {
		t.Fatalf("сервер: %v", err)
	}
	for range loginNameFails {
		s.loginLimits.failed("192.0.2.0/24", "a@b.c", time.Now())
	}
	h := s.Handler()

	cases := []struct {
		name, path, ctype, body string
	}{
		{"форма сайта", "/login", "application/x-www-form-urlencoded", "login=A@b.c&password=x"},
		{"расширение", "/api/auth/login", "application/json", `{"email":"a@b.c","password":"x"}`},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", c.ctype)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests {
			t.Errorf("%s: код %d, ожидался 429: %s", c.name, w.Code, w.Body.String())
		}
	}
}
