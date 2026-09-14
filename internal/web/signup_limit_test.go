package web

import (
	"testing"
	"time"
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
