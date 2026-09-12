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
		// Рут считается по своему пакету наравне со всеми: иначе он не смог
		// бы посмотреть на админку глазами обычного человека, а ради этого
		// он себе пакет и меняет. Заводится рут с ультра.
		{"рут с ультра", &User{Role: RoleRoot, Plan: PlanUltra}, 0},
		{"рут, снизивший себе пакет", &User{Role: RoleRoot, Plan: PlanBasic}, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.user.RelationLimit(); got != tt.want {
				t.Errorf("предел = %d, ожидался %d", got, tt.want)
			}
		})
	}
}

// «Когда и в какой партии видели» открыто с расширенного пакета. Проверка
// идёт через AtLeast, поэтому новый пакет поверх ультра получит её сам,
// а незнакомый — нет: он считается базовым.
func TestPlanCanSeeLastSeen(t *testing.T) {
	tests := []struct {
		plan Plan
		want bool
	}{
		{PlanBasic, false},
		{PlanExtended, true},
		{PlanUltra, true},
		{Plan("premium"), false},
	}
	for _, tt := range tests {
		if got := tt.plan.CanSeeLastSeen(); got != tt.want {
			t.Errorf("%q: видит = %v, ожидалось %v", tt.plan, got, tt.want)
		}
	}
}

// Роль поля не открывает: ни админу с базовым пакетом, ни руту, если тот
// снизил себе пакет нарочно.
func TestUserCanSeeLastSeen(t *testing.T) {
	tests := []struct {
		name string
		user *User
		want bool
	}{
		{"пользователь с базовым", &User{Role: RoleUser, Plan: PlanBasic}, false},
		{"админ с базовым", &User{Role: RoleAdmin, Plan: PlanBasic}, false},
		{"пользователь с расширенным", &User{Role: RoleUser, Plan: PlanExtended}, true},
		{"рут с ультра", &User{Role: RoleRoot, Plan: PlanUltra}, true},
		{"рут, снизивший себе пакет", &User{Role: RoleRoot, Plan: PlanBasic}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.user.CanSeeLastSeen(); got != tt.want {
				t.Errorf("видит = %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

// Карты анонимных партий — только ультра. Отличается от прочих
// возможностей порогом, поэтому проверяется отдельно: расширенный здесь
// уже не проходит.
func TestPlanCanSeeAnonymousMaps(t *testing.T) {
	tests := []struct {
		plan Plan
		want bool
	}{
		{PlanBasic, false},
		{PlanExtended, false},
		{PlanUltra, true},
		{Plan("premium"), false},
	}
	for _, tt := range tests {
		if got := tt.plan.CanSeeAnonymousMaps(); got != tt.want {
			t.Errorf("%q: пускает = %v, ожидалось %v", tt.plan, got, tt.want)
		}
	}
}

func TestUserCanSeeAnonymousMaps(t *testing.T) {
	tests := []struct {
		name string
		user *User
		want bool
	}{
		{"пользователь с расширенным", &User{Role: RoleUser, Plan: PlanExtended}, false},
		{"админ с расширенным", &User{Role: RoleAdmin, Plan: PlanExtended}, false},
		{"пользователь с ультра", &User{Role: RoleUser, Plan: PlanUltra}, true},
		{"рут с ультра", &User{Role: RoleRoot, Plan: PlanUltra}, true},
		// Рут, снизивший себе пакет, упирается в тот же запрет, что и все:
		// в этом весь смысл — увидеть админку чужими глазами.
		{"рут, снизивший себе пакет", &User{Role: RoleRoot, Plan: PlanBasic}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.user.CanSeeAnonymousMaps(); got != tt.want {
				t.Errorf("пускает = %v, ожидалось %v", got, tt.want)
			}
		})
	}
}
