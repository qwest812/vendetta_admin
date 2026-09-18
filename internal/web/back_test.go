package web

import (
	"net/url"
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
