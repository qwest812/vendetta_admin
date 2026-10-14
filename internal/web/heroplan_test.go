package web

import (
	"testing"
	"time"

	"Vendetta_admin/internal/supremacy"
)

// Значение формы плана переживает обратный разбор, в том числе имя
// с разделителем внутри; срок ноль — «день уже наступил».
func TestHeroPlanValueRoundTrip(t *testing.T) {
	opens := time.Unix(1792200000, 0)
	o := heroOption{HeroChoice: supremacy.HeroChoice{HeroItem: supremacy.HeroItem{Unit: 50621, Name: "Люсьен | Ларош"}},
		Day: 8, Opens: opens}
	unit, day, at, name, ok := parseHeroPlanValue(heroPlanValue(o))
	if !ok || unit != 50621 || day != 8 || !at.Equal(opens) || name != "Люсьен | Ларош" {
		t.Errorf("разбор: %d %d %v %q %v", unit, day, at, name, ok)
	}

	o.Opens = time.Time{}
	if _, _, at, _, ok := parseHeroPlanValue(heroPlanValue(o)); !ok || !at.IsZero() {
		t.Errorf("пустой срок разобрался как %v", at)
	}
	for _, bad := range []string{"", "x|8|0|Имя", "50621|8|0"} {
		if _, _, _, _, ok := parseHeroPlanValue(bad); ok {
			t.Errorf("%q разобрался", bad)
		}
	}
}
