package web

import (
	"testing"

	"Vendetta_admin/internal/supremacy"
	"Vendetta_admin/internal/supremacy/heroes"
)

// В выбор героя попадают только открытые (уровень не ноль) и не стоящие
// на карте; уровень считается по предметам уровней в инвентаре, портрет
// берётся из справочника героев.
func TestHeroOptions(t *testing.T) {
	state := &supremacy.GameState{Day: 3, Inventory: map[int]int{101: 1, 102: 1, 201: 0}}
	choices := []supremacy.HeroChoice{
		{HeroItem: supremacy.HeroItem{Unit: 23041, Item: 23047, Name: "Кангал"}},
		{HeroItem: supremacy.HeroItem{Unit: 50619, Item: 50672, Name: "Мас"}},
		{HeroItem: supremacy.HeroItem{Unit: 50617, Item: 50656, Name: "Мейв"}, OnMap: true},
	}
	cards := map[int]supremacy.HeroCard{
		23047: {Day: 2, Levels: []int{101, 102, 103}},
		50672: {Day: 1, Levels: []int{201}},
		50656: {Day: 2, Levels: []int{101}},
	}
	faces := &heroes.Snapshot{Heroes: []heroes.Hero{{UnitTypeID: 23041, Image: "orhan_kangal_demir.png"}}}

	got := heroOptions(choices, cards, state, faces)
	if len(got) != 1 {
		t.Fatalf("в выборе %d героев, ожидался один Кангал: %+v", len(got), got)
	}
	k := got[0]
	if k.Name != "Кангал" || k.Level != 2 || k.MaxLevel != 3 || k.Image != "orhan_kangal_demir.png" || !k.Ready {
		t.Errorf("Кангал в выборе: %+v", k)
	}
}
