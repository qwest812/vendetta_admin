package domain

import "testing"

func TestParseClanStatus(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  ClanStatus
		valid bool
	}{
		{"союзный", "ally", ClanAlly, true},
		{"нейтральный", "neutral", ClanNeutral, true},
		{"враждебный", " enemy ", ClanEnemy, true},
		// Пусто в фильтре поиска значит «любой клан», а не ошибку.
		{"пусто", "", "", false},
		{"мусор", "friend", "friend", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseClanStatus(tt.in)
			if ok != tt.valid {
				t.Errorf("признан валидным = %v, ожидалось %v", ok, tt.valid)
			}
			if ok && got != tt.want {
				t.Errorf("статус = %q, ожидался %q", got, tt.want)
			}
		})
	}
}

func TestClanStatusDisplay(t *testing.T) {
	if got := ClanEnemy.Title(); got != "Враждебный" {
		t.Errorf("название = %q", got)
	}
	// Клан без статуса в базе не появится, но пустое значение приходит с
	// игроком без клана — подписывать его надо нейтрально, а не пустотой.
	if got := ClanStatus("").Title(); got != "Нейтральный" {
		t.Errorf("название пустого статуса = %q", got)
	}
	if !ClanAlly.Marked() || !ClanEnemy.Marked() {
		t.Error("союзный и враждебный должны получать метку")
	}
	if ClanNeutral.Marked() {
		t.Error("нейтральный клан метить не нужно — их большинство")
	}
}
