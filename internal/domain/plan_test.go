package domain

import "testing"

// Пакеты вложены друг в друга, и предел должен расти вместе с ними.
// Числа здесь дублируют таблицу намеренно: молчаливая правка предела
// меняет то, что людям обещано на странице, и заметить её надо тестом.
func TestPlanRelationLimit(t *testing.T) {
	tests := []struct {
		plan Plan
		want int
	}{
		{PlanBasic, 20},
		{PlanExtended, 50},
		{PlanUltra, 0}, // без предела
		// Незнакомый пакет считаем базовым: в базе таких нет (колонка
		// с проверкой), но открыть лишнее хуже, чем недодать.
		{Plan("premium"), 20},
		{Plan(""), 20},
	}
	for _, tt := range tests {
		if got := tt.plan.RelationLimit(); got != tt.want {
			t.Errorf("%q: предел = %d, ожидался %d", tt.plan, got, tt.want)
		}
	}
}

func TestPlanAtLeast(t *testing.T) {
	if !PlanUltra.AtLeast(PlanBasic) || !PlanExtended.AtLeast(PlanExtended) {
		t.Error("старший пакет должен покрывать младший")
	}
	if PlanBasic.AtLeast(PlanExtended) {
		t.Error("базовый не должен считаться расширенным")
	}
}

func TestPlanValid(t *testing.T) {
	for _, p := range Plans {
		if !p.Valid() {
			t.Errorf("%q должен быть допустимым", p)
		}
	}
	if Plan("premium").Valid() {
		t.Error("выдуманный пакет не должен проходить проверку")
	}
}

// Подпись к пакету берётся по ключу; незнакомый пакет показывается кодом,
// а не выдуманным названием.
func TestPlanTitleKey(t *testing.T) {
	if got := PlanExtended.TitleKey(); got != "plan.extended" {
		t.Errorf("ключ = %q", got)
	}
	if got := Plan("premium").TitleKey(); got != "premium" {
		t.Errorf("незнакомый пакет = %q, ожидался его же код", got)
	}
}

// Предел человека — это предел его пакета, кроме рута: тот вне счёта,
// как и с проверками карты.
func TestUserRelationLimit(t *testing.T) {
	tests := []struct {
		name string
		user *User
		want int
	}{
		{"пользователь с базовым", &User{Role: RoleUser, Plan: PlanBasic}, 20},
		{"админ с базовым", &User{Role: RoleAdmin, Plan: PlanBasic}, 20},
		{"пользователь с ультра", &User{Role: RoleUser, Plan: PlanUltra}, 0},
		// Пакет у рута в базе есть, но ни на что не влияет.
		{"рут с базовым", &User{Role: RoleRoot, Plan: PlanBasic}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.user.RelationLimit(); got != tt.want {
				t.Errorf("предел = %d, ожидался %d", got, tt.want)
			}
		})
	}
}
