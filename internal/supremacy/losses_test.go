package supremacy

import (
	"encoding/json"
	"testing"
)

func TestParseCount(t *testing.T) {
	cases := map[string]int64{
		"12,400":  12400,
		"12 400":  12400,
		"12 400":  12400,
		"12.400":  12400,
		"1.2 M":   1200000,
		"1,2 млн": 1200000,
		"15k":     15000,
		"15 тыс.": 15000,
		"0":       0,
		"731":     731,
	}
	for in, want := range cases {
		got, ok := parseCount(in)
		if !ok || got != want {
			t.Errorf("parseCount(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	if _, ok := parseCount("много"); ok {
		t.Error("parseCount(много) должен отказать")
	}
}

// Сводку узнаём по двум строкам со странами, на любом языке; стороны
// упорядочены по номеру, чтобы пара была одна.
func TestParseWar(t *testing.T) {
	body := "Потери с начала этой войны:\n" +
		"{{{countryLink 'Франция' '7'}}} - 1,2 млн\n" +
		"{{{countryLink 'Германия' '3'}}} - 12 400\n"
	w, ok := parseWar(body)
	if !ok {
		t.Fatal("сводка не узнана")
	}
	if w.A != 3 || w.NameA != "Германия" || w.LostA != 12400 || w.B != 7 || w.LostB != 1200000 {
		t.Fatalf("сводка = %+v", w)
	}
	if _, ok := parseWar("{{{countryLink 'Франция' '7'}}} захватила провинцию"); ok {
		t.Error("статья без двух сторон — не сводка")
	}
}

// Мир обнуляет счёт войны, но за партию потери складываются.
func TestSumWarsBanksAcrossPeace(t *testing.T) {
	days := map[int]paperDay{
		1: {wars: []warLine{{A: 3, B: 7, NameA: "DE", NameB: "FR", LostA: 100, LostB: 50}}},
		2: {wars: []warLine{{A: 3, B: 7, NameA: "DE", NameB: "FR", LostA: 300, LostB: 80}}},
		// мир, новая война — счёт с нуля
		4: {wars: []warLine{{A: 3, B: 7, NameA: "DE", NameB: "FR", LostA: 20, LostB: 40}}},
		5: {wars: []warLine{{A: 3, B: 9, NameA: "DE", NameB: "PL", LostA: 5, LostB: 500}}},
	}
	list := sumWars(days)
	byID := map[int]PlayerLosses{}
	for _, p := range list {
		byID[p.ID] = p
	}
	de := byID[3]
	if de.Lost != 325 || de.Killed != 620 || len(de.Opponent) != 2 {
		t.Fatalf("Германия: %+v", de)
	}
	if de.Opponent[0].OpponentID != 7 || de.Opponent[0].Lost != 320 || de.Opponent[0].Killed != 120 {
		t.Fatalf("Германия против Франции: %+v", de.Opponent[0])
	}
	if fr := byID[7]; fr.Lost != 120 || fr.Killed != 320 {
		t.Fatalf("Франция: %+v", fr)
	}
	if list[0].ID != 9 {
		t.Fatalf("первым — кто потерял больше всех: %+v", list[0])
	}
}

func TestPaperArticlesShapes(t *testing.T) {
	art := `{"@c":"ultshared.UltArticle","title":"t","messageBody":"x"}`
	for _, raw := range []string{
		`{"day":3,"articles":[` + art + `]}`,
		`{"day":3,"articles":["java.util.ArrayList",[` + art + `]]}`,
		`{"states":{"2":{"day":3,"articles":[` + art + `]}}}`,
	} {
		got, err := paperArticles(json.RawMessage(raw))
		if err != nil || len(got) != 1 || got[0] != (paperArticle{Title: "t", Body: "x"}) {
			t.Errorf("%s → %v, %v", raw, got, err)
		}
	}
}
