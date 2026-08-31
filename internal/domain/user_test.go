package domain

import (
	"strings"
	"testing"
)

func TestCanManage(t *testing.T) {
	root := &User{ID: 1, Role: RoleRoot}
	admin := &User{ID: 2, Role: RoleAdmin}
	admin2 := &User{ID: 3, Role: RoleAdmin}
	user := &User{ID: 4, Role: RoleUser}

	cases := []struct {
		name   string
		actor  *User
		target *User
		want   bool
	}{
		{"рут управляет админом", root, admin, true},
		{"рут управляет пользователем", root, user, true},
		{"рут не управляет собой", root, root, false},
		{"админ управляет пользователем", admin, user, true},
		{"админ понижает другого админа", admin, admin2, true},
		{"админ не трогает рута", admin, root, false},
		{"админ не трогает себя", admin, admin, false},
		{"пользователь не управляет никем", user, admin, false},
		{"пользователь не управляет собой", user, user, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanManage(c.actor, c.target); got != c.want {
				t.Errorf("CanManage = %v, ожидалось %v", got, c.want)
			}
		})
	}
}

func TestRoleAtLeast(t *testing.T) {
	if !RoleRoot.AtLeast(RoleAdmin) {
		t.Error("рут должен проходить проверку на админа")
	}
	if RoleUser.AtLeast(RoleAdmin) {
		t.Error("пользователь не должен проходить проверку на админа")
	}
	if Role("guest").Valid() {
		t.Error("неизвестная роль не должна считаться валидной")
	}
}

func TestValidateNickname(t *testing.T) {
	valid := []string{"root", "Ярослав", "player_01", "a.b-c", "ник123"}
	for _, n := range valid {
		if err := ValidateNickname(n); err != nil {
			t.Errorf("ник %q должен проходить: %v", n, err)
		}
	}
	invalid := []string{"", "ab", "с пробелом", "user@mail.com", "_подчёркивание", strings.Repeat("a", 33)}
	for _, n := range invalid {
		if err := ValidateNickname(n); err == nil {
			t.Errorf("ник %q не должен проходить", n)
		}
	}
}

// Подпись в журнале и в авторе заметки не должна быть пустой,
// когда пользователь заведён без почты.
func TestUserDisplay(t *testing.T) {
	withEmail := &User{Email: "a@b.c", Nickname: "ник"}
	if got := withEmail.Display(); got != "a@b.c" {
		t.Errorf("Display = %q, ожидалось a@b.c", got)
	}
	noEmail := &User{Nickname: "ник"}
	if got := noEmail.Display(); got != "ник" {
		t.Errorf("Display = %q, ожидалось ник", got)
	}
}

// Профиль человек заполняет сам, поэтому проверки мягкие: пустое — норма,
// а вот пробел в игровом ID сломал бы поиск по нему.
func TestValidateProfile(t *testing.T) {
	tests := []struct {
		name              string
		full, city, gameI string
		wantErr           bool
	}{
		{"пустой профиль", "", "", "", false},
		{"обычный", "Ярослав", "Киев", "101408369", false},
		{"длинное имя", strings.Repeat("я", 101), "", "", true},
		{"длинный город", "", strings.Repeat("к", 101), "", true},
		{"длинный ID", "", "", strings.Repeat("1", 33), true},
		{"пробел в ID", "", "", "101 408", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile(tt.full, tt.city, tt.gameI)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProfile(%q, %q, %q) = %v", tt.full, tt.city, tt.gameI, err)
			}
		})
	}
}
