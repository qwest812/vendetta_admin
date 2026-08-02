package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("правильный-пароль-123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("неожиданный формат хеша: %s", hash)
	}
	if err := VerifyPassword("правильный-пароль-123", hash); err != nil {
		t.Errorf("верный пароль отклонён: %v", err)
	}
	if err := VerifyPassword("другой-пароль-123", hash); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("ожидалось ErrPasswordMismatch, получено %v", err)
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := HashPassword("одинаковый-пароль")
	b, _ := HashPassword("одинаковый-пароль")
	if a == b {
		t.Error("два хеша одного пароля совпали — соль не работает")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "не-хеш", "$argon2i$v=19$m=1,t=1,p=1$aaaa$bbbb", "$argon2id$v=99$m=1,t=1,p=1$aaaa$bbbb"} {
		if err := VerifyPassword("пароль", bad); err == nil {
			t.Errorf("испорченный хеш %q принят", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("короткий"); err == nil {
		t.Error("короткий пароль должен отклоняться")
	}
	if err := ValidatePassword("достаточно-длинный-пароль"); err != nil {
		t.Errorf("нормальный пароль отклонён: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 257)); err == nil {
		t.Error("слишком длинный пароль должен отклоняться")
	}
}
