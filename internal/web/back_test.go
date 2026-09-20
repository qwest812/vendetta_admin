package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Переключатель языка возвращает на ту же страницу, но за состоянием партии
// второй раз не ходит: state из адреса выпадает, остальные параметры
// остаются на месте.
func TestBackTo(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/games/10895766?state=1", "/games/10895766"},
		{"/games/10895766?state=1&mode=power", "/games/10895766?mode=power"},
		{"/games/10895766", "/games/10895766"},
		{"/?q=Vakyla", "/?q=Vakyla"},
		{"/games?id=10895766", "/games?id=10895766"},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.in)
		if err != nil {
			t.Fatalf("адрес %q не разобрался: %v", tt.in, err)
		}
		if got := backTo(u); got != tt.want {
			t.Errorf("из %q вышло %q, ожидалось %q", tt.in, got, tt.want)
		}
	}
}

// Заметку из личного списка правят и в разделе, и на карточке игрока,
// поэтому форма говорит, куда вернуться. Чужие адреса в это поле пускать
// нельзя: иначе наша же форма уводила бы человека на сторонний сайт.
func TestFormBack(t *testing.T) {
	const def = "/enemies"
	tests := []struct{ back, want string }{
		{"/players/12", "/players/12"},
		{"", def},
		{"/enemies", def},
		{"https://evil.example/players/12", def},
		{"//evil.example", def},
		{"/players/12/delete", def},
		{"/players/", def},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodPost, "/enemies/12/mark",
			strings.NewReader(url.Values{"back": {tt.back}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := formBack(r, def); got != tt.want {
			t.Errorf("из %q вышло %q, ожидалось %q", tt.back, got, tt.want)
		}
	}
}
