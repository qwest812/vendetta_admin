package web

import (
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// Игра отдаёт время строкой с секундами, а порядок игр — как ей удобно.
// Наверх ставим самую свежую партию: за ней и следят.
func TestGameViews(t *testing.T) {
	views := gameViews([]supremacy.Game{
		{GameID: "1", Title: "старая", State: "running", StartOfGame: "1785920128"},
		{GameID: "2", Title: "свежая", State: "running", StartOfGame: "1785999999"},
		{GameID: "3", Title: "без даты", State: "readytojoin", StartOfGame: "0"},
	})

	got := []string{views[0].ID, views[1].ID, views[2].ID}
	want := []string{"2", "1", "3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок игр = %v, ожидался %v", got, want)
		}
	}
	if !views[2].Started.IsZero() {
		t.Error("нулевое время должно оставаться нулевым, а не 1970 годом")
	}
	if views[0].State != "идёт" || views[2].State != "набор" {
		t.Errorf("состояния = %q и %q", views[0].State, views[2].State)
	}
}

// Незнакомое состояние лучше показать как есть, чем спрятать: список игр
// молча потерял бы смысл.
func TestGameStateKeepsUnknown(t *testing.T) {
	if got := gameState("paused"); got != "paused" {
		t.Errorf("gameState(paused) = %q", got)
	}
}

func TestUnixTime(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"1785920128", time.Unix(1785920128, 0)},
		{"0", time.Time{}},
		{"", time.Time{}},
		{"null", time.Time{}},
	}
	for _, tt := range tests {
		if got := unixTime(tt.in); !got.Equal(tt.want) {
			t.Errorf("unixTime(%q) = %v, ожидалось %v", tt.in, got, tt.want)
		}
	}
}

// Карта раскрашивается по владельцам из состояния: свой цвет у каждой
// страны, ничейная земля — общим цветом, свои провинции обведены.
func TestGameMapColors(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}, {X: 30, Y: 10}},
			3: {{X: 40, Y: 0}, {X: 50, Y: 0}, {X: 50, Y: 10}},
		},
	}
	state := &supremacy.GameState{
		Me: 12,
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Dau7er", Color: "rgb(230,190,140)"},
			29: {ID: 29, Nation: "Франция"},
		},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 12},
			{ID: 2, Name: "Париж", Owner: 29},
			{ID: 3, Name: "Ничьё"},
			// Провинции без очертаний просто не рисуются: файл карты общий
			// для всех партий и обновляется отдельно от них.
			{ID: 99, Name: "Неизвестная", Owner: 12},
		},
	}

	view := gameMap(geo, state, nil, nil, 12)
	if view.Width != 100 || len(view.Shapes) != 3 {
		t.Fatalf("карта %dx%d, фигур %d", view.Width, view.Height, len(view.Shapes))
	}

	// Цвет страны берётся из состояния, но приглушённым: чистый игровой
	// на карте слепит.
	mine := view.Shapes[0]
	if mine.Fill != fadeColor("rgb(230,190,140)") || mine.Stroke != mapOwnStroke {
		t.Errorf("своя провинция: fill=%q stroke=%q", mine.Fill, mine.Stroke)
	}
	if mine.Points != "0,0 10,0 10,10" {
		t.Errorf("точки = %q", mine.Points)
	}
	if !strings.Contains(mine.Title, "Афины") || !strings.Contains(mine.Title, "Dau7er") {
		t.Errorf("подпись = %q", mine.Title)
	}

	// У игрока без цвета в профиле — запасной, но не цвет ничейной земли:
	// иначе чужая страна выглядела бы свободной.
	if foreign := view.Shapes[1]; foreign.Fill != mapUnknownFill || foreign.Stroke != "" {
		t.Errorf("чужая провинция: fill=%q stroke=%q", foreign.Fill, foreign.Stroke)
	}
	if neutral := view.Shapes[2]; neutral.Fill != mapNeutralFill || neutral.Title != "Ничьё" {
		t.Errorf("ничейная провинция: fill=%q title=%q", neutral.Fill, neutral.Title)
	}

	// Подписи ставятся только там, где есть владелец: ничейная земля
	// остаётся без имени.
	if len(view.Labels) != 2 {
		t.Fatalf("подписей = %d, ожидалось две", len(view.Labels))
	}
	if view.Labels[0].Text != "Греция" || view.Labels[0].Enemy {
		t.Errorf("подпись своей страны = %+v", view.Labels[0])
	}
}

// Игрок партии, найденный в личном списке врагов, узнаётся по мечу
// у названия страны и по подсказке на провинции. Обводка только своя.
func TestGameMapMarksEnemies(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}, {X: 30, Y: 10}},
		},
	}
	state := &supremacy.GameState{
		Me: 12,
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Dau7er"},
			29: {ID: 29, Nation: "Франция", Name: "Враг", SiteUserID: "777"},
		},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 12},
			{ID: 2, Name: "Париж", Owner: 29},
		},
	}
	enemies := map[int]*domain.Player{29: {ID: 3, Nickname: "Враг", GameID: "777"}}

	view := gameMap(geo, state, enemies, nil, 12)

	foreign := view.Shapes[1]
	if foreign.Stroke != "" {
		t.Errorf("провинция врага обведена (%q), а обводка должна быть только своя", foreign.Stroke)
	}
	if !strings.Contains(foreign.Title, "во врагах") {
		t.Errorf("подпись провинции врага = %q", foreign.Title)
	}
	// Своя провинция обводится своим цветом, даже когда враги на карте есть.
	if view.Shapes[0].Stroke != mapOwnStroke {
		t.Errorf("своя провинция: stroke=%q", view.Shapes[0].Stroke)
	}

	var enemyLabel mapLabel
	for _, l := range view.Labels {
		if l.Enemy {
			enemyLabel = l
		}
	}
	// Значок хранится отдельно от названия: на странице он красится по-своему.
	if enemyLabel.Mark != "⚔" || enemyLabel.Text != "Франция" {
		t.Errorf("подпись страны врага = %+v", enemyLabel)
	}
}

// Смотрящий ищется по игровому ID из профиля, а не по аккаунту проекта:
// под общим аккаунтом в партию ходит админка.
func TestViewerPlayer(t *testing.T) {
	state := &supremacy.GameState{
		Me: 12,
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", SiteUserID: "101408369"},
			29: {ID: 29, Nation: "Франция", SiteUserID: "777"},
			31: {ID: 31, Nation: "Швеция"}, // компьютер, без номера на сайте
		},
	}

	if me, ok := viewerPlayer(state, "777"); !ok || me.ID != 29 {
		t.Errorf("по чужому ID = %+v, ok=%v; ожидался игрок 29", me, ok)
	}
	if _, ok := viewerPlayer(state, "555"); ok {
		t.Error("не участвующий в партии не должен находиться")
	}
	// Пустой ID в профиле не должен совпасть с игроками без номера на сайте.
	if _, ok := viewerPlayer(state, ""); ok {
		t.Error("пустой игровой ID не должен ни с кем совпадать")
	}
}

// Без своей страны на карте обводить нечего: ничейная земля (владелец 0)
// не должна принимать обводку за свою.
func TestGameMapWithoutViewer(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}, {X: 30, Y: 10}},
		},
	}
	state := &supremacy.GameState{
		Me:      12,
		Players: map[int]supremacy.Player{12: {ID: 12, Nation: "Греция", Color: "rgb(1,2,3)"}},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 12},
			{ID: 2, Name: "Ничьё"},
		},
	}

	view := gameMap(geo, state, nil, nil, 0)
	for i, shape := range view.Shapes {
		if shape.Stroke != "" {
			t.Errorf("фигура %d обведена (%q), хотя своей страны у смотрящего нет", i, shape.Stroke)
		}
	}
	// Аккаунт проекта на карте остаётся обычной страной со своим цветом.
	if view.Shapes[0].Fill != fadeColor("rgb(1,2,3)") {
		t.Errorf("страна аккаунта = %q", view.Shapes[0].Fill)
	}
}

// Друг из личного списка обводится зелёным и подписывается рукопожатием,
// а если игрок каким-то образом попал в оба списка — вражда важнее.
func TestGameMapMarksFriends(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}, {X: 30, Y: 10}},
			3: {{X: 40, Y: 0}, {X: 50, Y: 0}, {X: 50, Y: 10}},
		},
	}
	state := &supremacy.GameState{
		Me: 12,
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Dau7er"},
			29: {ID: 29, Nation: "Франция", Name: "Друг", SiteUserID: "777"},
			31: {ID: 31, Nation: "Швеция", Name: "И то и то", SiteUserID: "888"},
		},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 12},
			{ID: 2, Name: "Париж", Owner: 29},
			{ID: 3, Name: "Стокгольм", Owner: 31},
		},
	}
	friends := map[int]*domain.Player{
		29: {ID: 3, Nickname: "Друг", GameID: "777"},
		31: {ID: 4, Nickname: "И то и то", GameID: "888"},
	}
	enemies := map[int]*domain.Player{31: {ID: 4, Nickname: "И то и то", GameID: "888"}}

	view := gameMap(geo, state, enemies, friends, 12)

	if friend := view.Shapes[1]; friend.Stroke != "" {
		t.Errorf("провинция друга обведена: stroke=%q", friend.Stroke)
	}
	if !strings.Contains(view.Shapes[1].Title, "в друзьях") {
		t.Errorf("подпись провинции друга = %q", view.Shapes[1].Title)
	}
	// Спорная провинция тоже без обводки, а в подсказке — оба списка.
	if both := view.Shapes[2]; both.Stroke != "" {
		t.Errorf("провинция и друга, и врага: stroke=%q", both.Stroke)
	}
	if !strings.Contains(view.Shapes[2].Title, "в друзьях") ||
		!strings.Contains(view.Shapes[2].Title, "во врагах") {
		t.Errorf("подпись спорной провинции = %q", view.Shapes[2].Title)
	}
	// Своя провинция обводится своим цветом.
	if view.Shapes[0].Stroke != mapOwnStroke {
		t.Errorf("своя провинция: stroke=%q", view.Shapes[0].Stroke)
	}

	labels := map[string]mapLabel{}
	for _, l := range view.Labels {
		labels[l.Text] = l
	}
	if l, ok := labels["Франция"]; !ok || !l.Friend || l.Enemy || l.Mark != "🤝" {
		t.Errorf("подпись друга = %+v (все: %+v)", l, view.Labels)
	}
	if l, ok := labels["Швеция"]; !ok || !l.Enemy || l.Friend || l.Mark != "⚔" {
		t.Errorf("подпись спорной страны = %+v", l)
	}
}

// Цвета игра шлёт чистыми, а на карте они должны быть тише: каждый канал
// уводится к фону, чужая запись остаётся как есть.
func TestFadeColor(t *testing.T) {
	if got := fadeColor("rgb(230,190,140)"); got != "rgb(136,120,97)" {
		t.Errorf("приглушённый цвет = %q", got)
	}
	// Белый после подмеса перестаёт быть белым, но остаётся светлым.
	if got := fadeColor("rgb(255,255,255)"); got == "rgb(255,255,255)" {
		t.Errorf("белый не приглушился: %q", got)
	}
	for _, c := range []string{"", "красный", "#e8b45f", "rgb(1,2)", "rgb(1,2,300)"} {
		if got := fadeColor(c); got != c {
			t.Errorf("непонятный цвет %q превратился в %q", c, got)
		}
	}
}
