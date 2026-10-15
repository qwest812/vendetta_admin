package web

import (
	"bytes"
	"strings"
	"testing"

	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

func rl(id int, nation string, value float64) supremacy.RankLine {
	return supremacy.RankLine{PlayerID: id, Nation: nation, Value: value}
}

func rankState() *supremacy.GameState {
	return &supremacy.GameState{Day: 10, Players: map[int]supremacy.Player{
		2:  {ID: 2, Nation: "Франция", Name: "BigDominion"},
		43: {ID: 43, Nation: "Украина", Name: "MishaBlack"},
		30: {ID: 30, Nation: "Аравия", Name: "Dieux_66"},
	}}
}

// Ресурсы: тонны в сутки по последнему выпуску, страна — по номеру.
// Цвет линии закреплён за страной; выпавшая из рейтинга линия рвётся.
func TestRankingsResources(t *testing.T) {
	ranks := &supremacy.Rankings{Days: []supremacy.RankDay{
		{Day: 2, Economy: []supremacy.RankLine{rl(2, "Франция", 42402), rl(43, "Украина", 41847)}},
		{Day: 5, Fear: []supremacy.RankLine{rl(43, "Украина", 0)}}, // не тот рейтинг — дня нет
		{Day: 6, Economy: []supremacy.RankLine{rl(43, "Украина", 50000), rl(30, "Аравия", 38546)}},
		{Day: 10, Economy: []supremacy.RankLine{rl(2, "Франция", 61000), rl(43, "Украина", 60000)}},
	}}
	v := rankingsData(i18n.RU, rankRes, rankState(), ranks, 2)

	if v.Days != 3 || v.Day != 10 || len(v.Rows) != 2 {
		t.Fatalf("выпусков %d, последний %d, строк %d", v.Days, v.Day, len(v.Rows))
	}
	fr := v.Rows[0]
	if fr.Nation != "Франция" || fr.Name != "BigDominion" || fr.Value != "61 000 т/сут" || !fr.Mine ||
		fr.Series != 1 || fr.Color != resBands[len(resBands)-1] {
		t.Errorf("Франция: %+v", fr)
	}
	if !strings.Contains(string(v.Style), `#map-res:checked ~ .game-map polygon[data-owner="2"]`) {
		t.Errorf("заливка: %s", v.Style)
	}
	// Аравия выпала к последнему выпуску, но место на графике у неё есть.
	if len(v.Chart.Lines) != 3 || v.Chart.Lines[2].Label != "Аравия" {
		t.Fatalf("линии: %+v", v.Chart.Lines)
	}
	// Франции не было в 6-м дне — линия из двух кусков.
	if paths := v.Chart.Lines[0].Paths; len(paths) != 2 {
		t.Errorf("линия Франции не разорвана: %v", paths)
	}
	// Франция и Украина кончаются почти в одной точке — подписи разведены.
	if d := v.Chart.Lines[1].End.Y - v.Chart.Lines[0].End.Y; d < rankLabelGap && -d < rankLabelGap {
		t.Errorf("подписи наезжают: %v и %v", v.Chart.Lines[0].End, v.Chart.Lines[1].End)
	}
}

// Устрашение: место — порядок строки, первое — темнее всех, на графике
// первое место сверху.
func TestRankingsFear(t *testing.T) {
	ranks := &supremacy.Rankings{Days: []supremacy.RankDay{
		{Day: 5, Fear: []supremacy.RankLine{rl(2, "Франция", 0), rl(43, "Украина", 0)}},
		{Day: 9, Fear: []supremacy.RankLine{rl(43, "Украина", 0), rl(2, "Франция", 0)}},
	}}
	v := rankingsData(i18n.RU, rankFear, rankState(), ranks, 0)
	if v.Rows[0].Nation != "Украина" || v.Rows[0].Value != "1-е место" || v.Rows[0].Color != fearBands[len(fearBands)-1] {
		t.Errorf("первое место: %+v", v.Rows[0])
	}
	ua := v.Chart.Lines[0]
	if ua.Label != "Украина" || ua.Dots[1].Y >= ua.Dots[0].Y {
		t.Errorf("Украина поднялась со 2-го на 1-е — точка должна стать выше: %+v", ua.Dots)
	}

	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("шаблоны: %v", err)
	}
	var buf bytes.Buffer
	if err := pages["game"].ExecuteTemplate(&buf, "gamerankings",
		map[string]any{"Ranks": v, "GameID": "1", "Kind": rankFear}); err != nil {
		t.Fatalf("отрисовка: %v", err)
	}
	for _, want := range []string{"Украина", "series-1", "<title>Украина, день 9: 1-е место</title>", "kind=fear"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в блоке нет %q", want)
		}
	}
}

func TestNiceCeil(t *testing.T) {
	for v, want := range map[float64]float64{42402: 50000, 61000: 80000, 8200: 10000, 3: 4} {
		if got := niceCeil(v); got != want {
			t.Errorf("niceCeil(%v) = %v, ожидалось %v", v, got, want)
		}
	}
}

// Армии: доля в процентах, как пишет газета, больше — лучше и темнее.
func TestRankingsArmy(t *testing.T) {
	ranks := &supremacy.Rankings{Days: []supremacy.RankDay{
		{Day: 4, Army: []supremacy.RankLine{rl(6, "Швеция", 6.45), rl(43, "Украина", 5.68)}},
		{Day: 8, Army: []supremacy.RankLine{rl(43, "Украина", 9.09), rl(6, "Швеция", 7.5)}},
	}}
	v := rankingsData(i18n.RU, rankArmy, rankState(), ranks, 0)
	if v.Days != 2 || v.Rows[0].Nation != "Украина" || v.Rows[0].Value != "9,09%" ||
		v.Rows[0].Color != armyBands[len(armyBands)-1] {
		t.Errorf("армии: %+v", v.Rows)
	}
	if !strings.Contains(string(v.Style), `#map-army:checked`) {
		t.Errorf("заливка: %s", v.Style)
	}
}
