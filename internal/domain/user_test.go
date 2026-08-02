package domain

import "testing"

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
