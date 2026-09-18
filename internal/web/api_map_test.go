package web

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
)

// Мусор в составе к базе не идёт: номера на сайте — только цифры,
// номера в партии — положительные и без повторов.
func TestMapStateRejectsJunk(t *testing.T) {
	ok := apiMapRequest{Me: "11", Players: []apiMapPlayer{{ID: 1, Site: "11", Provinces: 3}}}
	if _, good := mapState(ok); !good {
		t.Fatal("правильный состав отвергнут")
	}
	bad := map[string]apiMapRequest{
		"пустой состав":       {Me: "11"},
		"свой номер буквами":  {Me: "x", Players: ok.Players},
		"чужой номер буквами": {Me: "11", Players: []apiMapPlayer{{ID: 1, Site: "1;drop"}}},
		"номер в партии ноль": {Me: "11", Players: []apiMapPlayer{{ID: 0, Site: "11"}}},
		"повтор":              {Me: "11", Players: []apiMapPlayer{{ID: 1}, {ID: 1}}},
		"провинций без меры":  {Me: "11", Players: []apiMapPlayer{{ID: 1, Provinces: 1 << 30}}},
		"коалиция без номера": {Me: "11", Players: ok.Players, Teams: []apiMapTeam{{ID: 0}}},
	}
	for name, req := range bad {
		if _, good := mapState(req); good {
			t.Errorf("%s: принято", name)
		}
	}
}

func TestGameColor(t *testing.T) {
	for in, want := range map[string]string{
		"#a2453c":      "rgba(162,69,60,255)",
		"rgb(1, 2, 3)": "rgba(1,2,3,255)",
		"":             "rgba(74,91,104,255)", // серый
		"rgb(1,2,999)": "rgba(74,91,104,255)",
	} {
		if got := gameColor(in); got != want {
			t.Errorf("%q → %q, ожидалось %q", in, got, want)
		}
	}
}

// Режимы расширения красят так же, как карта в админке: враг побеждает
// друга, бан — подписку, коалиция берёт цвет из игры, альянсы — палитру
// по владениям. Бот без номера на сайте — компьютерный.
func TestMapModes(t *testing.T) {
	req := apiMapRequest{
		Me: "100",
		Players: []apiMapPlayer{
			{ID: 1, Site: "100", Provinces: 10, Team: 7},
			{ID: 2, Site: "200", Provinces: 5, Premium: true, Banned: true},
			{ID: 3, Provinces: 2},
		},
		Teams: []apiMapTeam{{ID: 7, Name: "Север", Color: "rgba(10,20,30,255)"}},
	}
	state, ok := mapState(req)
	if !ok {
		t.Fatal("состав не принят")
	}
	card := &domain.Player{ID: 9}
	sides := &gameSides{
		Enemies:   map[int]*domain.Player{2: card},
		Friends:   map[int]*domain.Player{2: card},
		Alliances: map[int]domain.Alliance{1: {ID: "55", Name: "Волки"}},
	}
	modes := mapModes(i18n.RU, state, sides, 1, 0, false)
	byKey := map[string]apiMode{}
	for _, m := range modes {
		byKey[m.Key] = m
	}
	if len(modes) != 6 {
		t.Fatalf("режимов %d, ожидалось 6", len(modes))
	}
	check := func(mode string, player int, want string) {
		t.Helper()
		if got := byKey[mode].Colors[player]; got != want {
			t.Errorf("%s, игрок %d: %q, ожидалось %q", mode, player, got, want)
		}
	}
	check("clans", 1, gameColor(mapPalette[0]))
	check("clans", 2, gameColor(mapGrey))
	check("sides", 2, gameColor(mapSideEnemy))
	check("teams", 1, "rgba(10,20,30,255)")
	check("players", 2, gameColor(mapBanned))
	check("players", 3, gameColor(mapAI))
	check("players", 1, gameColor(mapHuman))
	// Счёта нет ни у кого — «Сила» серая и говорит почему.
	check("power", 2, gameColor(mapGrey))
	if byKey["power"].Status == "" {
		t.Error("«Сила» без счёта молчит о причине")
	}
	if byKey["top"].Status == "" {
		t.Error("топ без снимка молчит о причине")
	}
	if got := byKey["teams"].Legend; len(got) != 1 || !strings.Contains(got[0].Label, "ваша") {
		t.Errorf("легенда коалиций: %+v", got)
	}
}

// Цвета режимов с классами живут и в app.css, и здесь. Разойдутся — в игре
// и в админке один режим покрасит по-разному.
func TestMapColorsMatchStylesheet(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for sel, color := range map[string]string{
		`.game-map polygon {`:       mapGrey,
		`polygon.side-friend {`:     mapSideFriend,
		`polygon.side-enemy {`:      mapSideEnemy,
		`polygon.human {`:           mapHuman,
		`polygon.ai {`:              mapAI,
		`polygon.premium {`:         mapPremium,
		`polygon.banned {`:          mapBanned,
		`polygon.power-much-up {`:   mapPowerMuchUp,
		`polygon.power-up {`:        mapPowerUp,
		`polygon.power-even {`:      mapPowerEven,
		`polygon.power-down {`:      mapPowerDown,
		`polygon.power-much-down {`: mapPowerMuchDn,
	} {
		re := regexp.MustCompile(regexp.QuoteMeta(sel) + `\s*fill:\s*(#[0-9a-f]{6})`)
		m := re.FindSubmatch(css)
		if m == nil {
			t.Errorf("в app.css нет правила %s", sel)
			continue
		}
		if string(m[1]) != color {
			t.Errorf("%s: в стилях %s, в коде %s", sel, m[1], color)
		}
	}
}
