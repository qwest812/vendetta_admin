package web

import (
	"strings"
	"testing"

	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Имена — из партии, если игрок там есть; цвет — по доле от самого
// пострадавшего; ушедшие с карты остаются в таблице с пометкой.
func TestLossesData(t *testing.T) {
	state := &supremacy.GameState{
		Players:   map[int]supremacy.Player{3: {ID: 3, Nation: "Германия", Name: "Dau7er"}},
		Provinces: []supremacy.Province{{ID: 1, Owner: 3}},
	}
	losses := &supremacy.Losses{Days: 5, Missing: []int{2}, Players: []supremacy.PlayerLosses{
		{ID: 7, Name: "Франция", Lost: 1200000, Killed: 12400,
			Opponent: []supremacy.Duel{{OpponentID: 3, OpponentName: "DE", Lost: 1200000, Killed: 12400}}},
		{ID: 3, Name: "DE", Lost: 12400, Killed: 1200000},
	}}
	v := lossesData(i18n.RU, state, losses, 3)
	if len(v.Rows) != 2 || v.Missing != "2" {
		t.Fatalf("view = %+v", v)
	}
	fr, de := v.Rows[0], v.Rows[1]
	if fr.Nation != "Франция" || fr.OnMap || fr.Lost != "1 200 000" || fr.Ratio != "0.01" {
		t.Fatalf("Франция: %+v", fr)
	}
	if fr.Opponents[0].Nation != "Германия" {
		t.Fatalf("противник должен называться по партии: %+v", fr.Opponents)
	}
	if !de.Mine || !de.OnMap || de.Name != "Dau7er" || de.Color != lossesBands[0] {
		t.Fatalf("Германия: %+v", de)
	}
	if !strings.Contains(string(v.Style), `polygon[data-owner="7"] { fill: #6e1f1a; }`) {
		t.Fatalf("css: %s", v.Style)
	}
}
