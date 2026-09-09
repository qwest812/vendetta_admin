package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Игра отдаёт время строкой с секундами, а порядок игр — как ей удобно.
// Наверх ставим самую свежую партию: за ней и следят.
func TestGameViews(t *testing.T) {
	views := gameViews(i18n.RU, []supremacy.Game{
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
	if got := gameState(i18n.RU, "paused"); got != "paused" {
		t.Errorf("gameState(i18n.RU, paused) = %q", got)
	}
}

// Карта раскрашивается по кланам: клан владельца из базы даёт цвет,
// остальное — серым, свои провинции обведены.
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

	sides := &gameSides{Alliances: map[int]domain.Alliance{
		12: {SiteUserID: "101408369", ID: "843930", Name: "VEN.DETTA", Tag: "-V.D-"},
	}}

	view := gameMap(i18n.RU, geo, state, sides, 12)
	if view.Width != 100 || len(view.Shapes) != 3 {
		t.Fatalf("карта %dx%d, фигур %d", view.Width, view.Height, len(view.Shapes))
	}

	// Цвет даёт клан из базы, а не собственный цвет страны в игре, и едет
	// он переменной: заливку выбирает режим карты уже в css.
	mine := view.Shapes[0]
	if mine.Class != "clan human own" || string(mine.ClanColor) != mapPalette[0] {
		t.Errorf("своя провинция: class=%q clan=%q", mine.Class, mine.ClanColor)
	}
	if mine.Points != "0,0 10,0 10,10" {
		t.Errorf("точки = %q", mine.Points)
	}
	if !strings.Contains(mine.Title, "Афины") || !strings.Contains(mine.Title, "Dau7er") ||
		!strings.Contains(mine.Title, "клан VEN.DETTA") {
		t.Errorf("подпись = %q", mine.Title)
	}

	// У игрока, которого нет в базе, остаётся один класс — род владельца:
	// клана мы про него не знаем, и в режиме кланов его закрасит серый
	// по умолчанию.
	if foreign := view.Shapes[1]; foreign.Class != "human" || foreign.ClanColor != "" {
		t.Errorf("чужая провинция: class=%q clan=%q", foreign.Class, foreign.ClanColor)
	}
	// Ничейная земля помечена отдельно: она серая в обоих режимах, но темнее
	// занятой — свободную не должно быть видно как чью-то.
	if neutral := view.Shapes[2]; neutral.Class != "neutral" || neutral.Title != "Ничьё" {
		t.Errorf("ничейная провинция: class=%q title=%q", neutral.Class, neutral.Title)
	}

	// Подписи ставятся только там, где есть владелец: ничейная земля
	// остаётся без имени.
	if len(view.Labels) != 2 {
		t.Fatalf("подписей = %d, ожидалось две", len(view.Labels))
	}
	if view.Labels[0].Text != "Греция" || view.Labels[0].Enemy {
		t.Errorf("подпись своей страны = %+v", view.Labels[0])
	}
	// Ник идёт второй строкой под названием страны.
	if view.Labels[0].Nick != "Dau7er" {
		t.Errorf("ник под страной = %q", view.Labels[0].Nick)
	}
	// У бота ника нет, и пустой строки под страной быть не должно.
	if view.Labels[1].Nick != "" {
		t.Errorf("под страной без игрока подписано %q", view.Labels[1].Nick)
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
	enemy := &domain.Player{ID: 3, Nickname: "Враг", GameID: "777"}
	sides := &gameSides{
		Cards:   map[int]*domain.Player{29: enemy},
		Enemies: map[int]*domain.Player{29: enemy},
	}

	view := gameMap(i18n.RU, geo, state, sides, 12)

	foreign := view.Shapes[1]
	if strings.Contains(foreign.Class, "own") {
		t.Errorf("провинция врага помечена своей: class=%q", foreign.Class)
	}
	if !strings.Contains(foreign.Class, "side-enemy") {
		t.Errorf("классы провинции врага = %q", foreign.Class)
	}
	if !strings.Contains(foreign.Title, "во врагах") {
		t.Errorf("подпись провинции врага = %q", foreign.Title)
	}
	// Своя провинция помечается своей, даже когда враги на карте есть.
	if !strings.Contains(view.Shapes[0].Class, "own") {
		t.Errorf("своя провинция: class=%q", view.Shapes[0].Class)
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

	view := gameMap(i18n.RU, geo, state, nil, 0)
	for i, shape := range view.Shapes {
		if strings.Contains(shape.Class, "own") {
			t.Errorf("фигура %d помечена своей (%q), хотя своей страны у смотрящего нет", i, shape.Class)
		}
	}
	// Аккаунт проекта на карте — обычная страна: карточки в базе у него
	// нет, ни в какой клан и коалицию он не входит, и остаётся при нём
	// только род владельца — живой игрок.
	if view.Shapes[0].Class != "human" {
		t.Errorf("страна аккаунта = %q", view.Shapes[0].Class)
	}
	if view.Legend != nil {
		t.Errorf("легенда без единого клана = %+v", view.Legend)
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
	friend := &domain.Player{ID: 3, Nickname: "Друг", GameID: "777"}
	both := &domain.Player{ID: 4, Nickname: "И то и то", GameID: "888"}
	sides := &gameSides{
		Cards:   map[int]*domain.Player{29: friend, 31: both},
		Friends: map[int]*domain.Player{29: friend, 31: both},
		Enemies: map[int]*domain.Player{31: both},
	}

	view := gameMap(i18n.RU, geo, state, sides, 12)

	if got := view.Shapes[1].Class; got != "side-friend human" {
		t.Errorf("классы провинции друга = %q", got)
	}
	// Спорная провинция несёт оба класса, а какой победит — решает порядок
	// правил в app.css: там вражда стоит ниже дружбы.
	if got := view.Shapes[2].Class; got != "side-friend side-enemy human" {
		t.Errorf("классы спорной провинции = %q", got)
	}
	if !strings.Contains(view.Shapes[1].Title, "в друзьях") {
		t.Errorf("подпись провинции друга = %q", view.Shapes[1].Title)
	}
	if !strings.Contains(view.Shapes[2].Title, "в друзьях") ||
		!strings.Contains(view.Shapes[2].Title, "во врагах") {
		t.Errorf("подпись спорной провинции = %q", view.Shapes[2].Title)
	}
	// Своя провинция помечается своей.
	if !strings.Contains(view.Shapes[0].Class, "own") {
		t.Errorf("своя провинция: class=%q", view.Shapes[0].Class)
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

// Цвет клану даёт место в палитре, а место — число провинций: крупнейший
// клан партии берёт первый цвет. Клан, которому цвета не хватило, рисуется
// общим серым, но из легенды не пропадает.
func TestAllianceFills(t *testing.T) {
	// Кланов на один больше, чем цветов, и каждый следующий мельче
	// предыдущего: так проверяется и порядок, и нехватка палитры.
	clans := len(mapPalette) + 1
	state := &supremacy.GameState{Players: map[int]supremacy.Player{}}
	alliances := map[int]domain.Alliance{}
	for i := range clans {
		playerID := i + 1
		state.Players[playerID] = supremacy.Player{ID: playerID}
		alliances[playerID] = domain.Alliance{
			ID:   fmt.Sprintf("%d", playerID),
			Name: fmt.Sprintf("клан %d", playerID),
			Tag:  fmt.Sprintf("K%d", playerID),
		}
		for range clans - i {
			state.Provinces = append(state.Provinces,
				supremacy.Province{ID: len(state.Provinces) + 1, Owner: playerID})
		}
	}
	// Ничейная земля в счёт клана не идёт.
	state.Provinces = append(state.Provinces, supremacy.Province{ID: 999})

	fills, legend := allianceFills(i18n.RU, state, alliances)
	if len(legend) != clans {
		t.Fatalf("в легенде %d кланов, ожидалось %d", len(legend), clans)
	}
	for i, row := range legend {
		if want := clans - i; row.Provinces != want {
			t.Errorf("клан %q: провинций %d, ожидалось %d", row.Name, row.Provinces, want)
		}
		playerID := i + 1
		if i < len(mapPalette) {
			if row.Color != mapPalette[i] || fills[playerID] != mapPalette[i] {
				t.Errorf("клан %q: цвет %q и заливка %q, ожидался %q",
					row.Name, row.Color, fills[playerID], mapPalette[i])
			}
			continue
		}
		// Последнему цвета не досталось: заливки нет вовсе, карта нарисует
		// его тем же серым, что и всех без клана.
		if row.Color != "" {
			t.Errorf("лишний клан %q получил цвет %q", row.Name, row.Color)
		}
		if _, ok := fills[playerID]; ok {
			t.Errorf("лишний клан %q попал в заливку: %q", row.Name, fills[playerID])
		}
	}
}

// Игрок, которого сайт вернул без клана, цвета не получает, и легенды
// тогда нет вовсе: красить карту не во что. Пустой ID — это ответ игры
// «ни в каком клане не состоит», а не «не спрашивали».
func TestAllianceFillsWithoutAlliances(t *testing.T) {
	state := &supremacy.GameState{
		Players:   map[int]supremacy.Player{12: {ID: 12}},
		Provinces: []supremacy.Province{{ID: 1, Owner: 12}},
	}
	alliances := map[int]domain.Alliance{12: {SiteUserID: "777"}}

	if fills, legend := allianceFills(i18n.RU, state, alliances); len(fills) != 0 || legend != nil {
		t.Errorf("заливка = %v, легенда = %v", fills, legend)
	}
}

// Коалиции приходят из самой партии: и название, и цвет у них свои,
// придумывать приходится только там, где игра цвета не дала. Крупнейшая
// коалиция идёт в легенде первой, своя — помечается.
func TestTeamFills(t *testing.T) {
	state := &supremacy.GameState{
		Players: map[int]supremacy.Player{
			12: {ID: 12, TeamID: 3},
			29: {ID: 29, TeamID: 3},
			31: {ID: 31, TeamID: 5},
			// Одиночка и участник распущенной коалиции неотличимы: у первого
			// ноль, у второго номер, которого нет в списке живых.
			33: {ID: 33},
			34: {ID: 34, TeamID: 9},
		},
		Teams: map[int]supremacy.Team{
			3: {ID: 3, Name: "КСГ", Color: "rgb(120,90,120)"},
			5: {ID: 5, Name: "Одиночки"},
		},
	}

	fills, legend := teamFills(i18n.RU, state, 12)

	if len(legend) != 2 || legend[0].Name != "КСГ" || legend[0].Members != 2 || !legend[0].Mine {
		t.Fatalf("легенда = %+v", legend)
	}
	if legend[0].Color != "rgb(120,90,120)" {
		t.Errorf("цвет коалиции = %q, ожидался игровой", legend[0].Color)
	}
	// Игра дала коалицию без цвета — берём свой из палитры, иначе обводки
	// у неё не будет вовсе.
	if legend[1].Color != mapPalette[1] || legend[1].Mine {
		t.Errorf("коалиция без цвета = %+v", legend[1])
	}
	if fills[12] != legend[0].Color || fills[29] != legend[0].Color {
		t.Errorf("заливка участников = %q и %q", fills[12], fills[29])
	}
	for _, id := range []int{33, 34} {
		if color, ok := fills[id]; ok {
			t.Errorf("игрок %d вне живой коалиции получил цвет %q", id, color)
		}
	}
}

// Без коалиций в партии режим карты нечем наполнять — и легенды тогда нет.
func TestTeamFillsWithoutTeams(t *testing.T) {
	state := &supremacy.GameState{Players: map[int]supremacy.Player{12: {ID: 12, TeamID: 3}}}
	if fills, legend := teamFills(i18n.RU, state, 12); len(fills) != 0 || legend != nil {
		t.Errorf("заливки = %v, легенда = %v", fills, legend)
	}
}

// Коалиция красит ту же провинцию, что и клан, и попадает в подсказку:
// режимы не мешают друг другу, потому что признаки живут в классах,
// а выбирает из них css.
func TestGameMapFillsTeams(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}}},
	}
	state := &supremacy.GameState{
		Me:        12,
		Players:   map[int]supremacy.Player{12: {ID: 12, Nation: "Греция", TeamID: 3, Premium: true}},
		Teams:     map[int]supremacy.Team{3: {ID: 3, Name: "КСГ", Color: "rgb(120,90,120)"}},
		Provinces: []supremacy.Province{{ID: 1, Name: "Афины", Owner: 12}},
	}
	sides := &gameSides{Alliances: map[int]domain.Alliance{
		12: {SiteUserID: "101408369", ID: "843930", Name: "VEN.DETTA"},
	}}

	view := gameMap(i18n.RU, geo, state, sides, 12)

	shape := view.Shapes[0]
	if shape.Class != "clan team human premium own" {
		t.Errorf("классы провинции = %q", shape.Class)
	}
	if string(shape.TeamColor) != "rgb(120,90,120)" || string(shape.ClanColor) != mapPalette[0] {
		t.Errorf("цвета провинции: team=%q clan=%q", shape.TeamColor, shape.ClanColor)
	}
	if !strings.Contains(shape.Title, "клан VEN.DETTA") ||
		!strings.Contains(shape.Title, "коалиция КСГ") ||
		!strings.Contains(shape.Title, "премиум") {
		t.Errorf("подсказка = %q", shape.Title)
	}
	if len(view.Teams) != 1 || !view.Teams[0].Mine {
		t.Errorf("легенда коалиций = %+v", view.Teams)
	}
}

// Режим «Игроки» красит землю по её владельцу, и родов там четыре.
// Классы провинция несёт все сразу, а спор между ними разбирает css;
// счёт легенды разбирает его так же, поэтому проверяются они вместе.
func TestGameMapPlayerKinds(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}, {X: 30, Y: 10}},
			3: {{X: 40, Y: 0}, {X: 50, Y: 0}, {X: 50, Y: 10}},
			4: {{X: 60, Y: 0}, {X: 70, Y: 0}, {X: 70, Y: 10}},
			5: {{X: 80, Y: 0}, {X: 90, Y: 0}, {X: 90, Y: 10}},
			6: {{X: 0, Y: 20}, {X: 10, Y: 20}, {X: 10, Y: 30}},
		},
	}
	state := &supremacy.GameState{
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Обычный"},
			29: {ID: 29, Nation: "Франция", IsAI: true},
			31: {ID: 31, Nation: "Швеция", Name: "Богатый", Premium: true},
			33: {ID: 33, Nation: "Бельгия", Name: "Мультовод", Banned: true},
			35: {ID: 35, Nation: "Италия", Name: "И то и то", Premium: true, Banned: true},
		},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 12},
			{ID: 2, Name: "Париж", Owner: 29},
			{ID: 3, Name: "Стокгольм", Owner: 31},
			{ID: 4, Name: "Брюссель", Owner: 33},
			{ID: 5, Name: "Рим", Owner: 35},
			// Вторая провинция того же человека: легенда считает людей,
			// а не земли, и от неё числа меняться не должны.
			{ID: 6, Name: "Милан", Owner: 35},
			{ID: 7, Name: "Ничьё"},
		},
	}

	view := gameMap(i18n.RU, geo, state, nil, 0)

	want := []string{"human", "ai", "human premium", "human banned", "human premium banned"}
	for i, w := range want {
		if got := view.Shapes[i].Class; got != w {
			t.Errorf("классы провинции %d = %q, ожидались %q", i, got, w)
		}
	}
	// Забаненного с премиумом легенда считает забаненным: в css его
	// правило стоит ниже и побеждает, а расходиться им нельзя.
	if got := (playerKinds{Regular: 1, AI: 1, Premium: 1, Banned: 2}); view.Kinds != got {
		t.Errorf("счёт владельцев = %+v, ожидался %+v", view.Kinds, got)
	}
}

// Режим «Сила» сравнивает владельца провинции со смотрящим, а не игроков
// между собой. Полосы решает отношение опасностей, и разобрать их надо
// вместе со счётом легенды: расходиться им нельзя.
func TestGameMapPower(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}, {X: 30, Y: 10}},
			3: {{X: 40, Y: 0}, {X: 50, Y: 0}, {X: 50, Y: 10}},
			4: {{X: 60, Y: 0}, {X: 70, Y: 0}, {X: 70, Y: 10}},
			5: {{X: 80, Y: 0}, {X: 90, Y: 0}, {X: 90, Y: 10}},
			6: {{X: 0, Y: 20}, {X: 10, Y: 20}, {X: 10, Y: 30}},
			7: {{X: 20, Y: 20}, {X: 30, Y: 20}, {X: 30, Y: 30}},
		},
	}
	state := &supremacy.GameState{
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Мы"},
			29: {ID: 29, Nation: "Франция", Name: "Зверь"},
			31: {ID: 31, Nation: "Швеция", Name: "Крепкий"},
			33: {ID: 33, Nation: "Бельгия", Name: "Ровня"},
			35: {ID: 35, Nation: "Италия", Name: "Слабее"},
			37: {ID: 37, Nation: "Испания", Name: "Неспрошенный"},
			39: {ID: 39, Nation: "Турция", IsAI: true},
		},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 12},
			{ID: 2, Name: "Париж", Owner: 29},
			{ID: 3, Name: "Стокгольм", Owner: 31},
			{ID: 4, Name: "Брюссель", Owner: 33},
			{ID: 5, Name: "Рим", Owner: 35},
			{ID: 6, Name: "Мадрид", Owner: 37},
			{ID: 7, Name: "Анкара", Owner: 39},
		},
	}

	// Опасность смотрящего — 20 уровень при кд 1.0. Остальные расставлены
	// по полосам вокруг неё: 40, 25, 20, 12 и 8.
	me := domain.UserStats{Level: 20, Defeated: 100, Casualties: 100}
	stats := map[int]domain.UserStats{
		12: me,
		29: {Level: 20, Defeated: 200, Casualties: 100},
		31: {Level: 25, Defeated: 100, Casualties: 100},
		33: {Level: 20, Defeated: 100, Casualties: 100},
		35: {Level: 12, Defeated: 100, Casualties: 100},
		37: {},
	}
	sides := &gameSides{Stats: stats, Me: me}

	view := gameMap(i18n.RU, geo, state, sides, 12)

	want := []string{"human power-even own", "human power-much-up", "human power-up",
		"human power-even", "human power-down", "human", "ai"}
	for i, w := range want {
		if got := view.Shapes[i].Class; got != w {
			t.Errorf("классы провинции %d = %q, ожидались %q", i, got, w)
		}
	}
	// Уровень и кд стоят в подсказке всегда: по ним и видно, почему
	// провинция такого цвета.
	if !strings.Contains(view.Shapes[1].Title, "20 уровень, кд 2.00") {
		t.Errorf("подсказка сильного = %q", view.Shapes[1].Title)
	}
	// Про неспрошенного в подсказке молчим: нулями его оболгать легко.
	if strings.Contains(view.Shapes[5].Title, "кд") {
		t.Errorf("подсказка неспрошенного = %q", view.Shapes[5].Title)
	}

	// Компьютерный владелец в легенду не идёт: счёта у бота не бывает,
	// и в строке «счёта ещё нет» он читался бы как незаконченная работа.
	got := powerLegend{MuchUp: 1, Up: 1, Even: 2, Down: 1, Unknown: 1, Me: me}
	if view.Power != got {
		t.Errorf("легенда силы = %+v, ожидалась %+v", view.Power, got)
	}
}

// Без своего счёта сравнивать не с чем, и красить нельзя никого: серая
// карта честнее выдуманных полос.
func TestGameMapPowerWithoutViewerStats(t *testing.T) {
	geo := &supremacy.MapGeometry{Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{1: {{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}}}}
	state := &supremacy.GameState{
		Players:   map[int]supremacy.Player{29: {ID: 29, Nation: "Франция"}},
		Provinces: []supremacy.Province{{ID: 1, Name: "Париж", Owner: 29}},
	}
	sides := &gameSides{Stats: map[int]domain.UserStats{29: {Level: 20, Defeated: 100, Casualties: 50}}}

	view := gameMap(i18n.RU, geo, state, sides, 0)

	if got := view.Shapes[0].Class; got != "human" {
		t.Errorf("классы провинции = %q, полос быть не должно", got)
	}
	if view.Power.Unknown != 1 {
		t.Errorf("легенда силы = %+v, ожидался один неизвестный", view.Power)
	}
	// Счёт самого игрока при этом известен, и в подсказке ему самое место.
	if !strings.Contains(view.Shapes[0].Title, "кд 2.00") {
		t.Errorf("подсказка = %q", view.Shapes[0].Title)
	}
}

// Премиум игра сообщает про всех участников партии, и это единственное
// место, где чужую подписку видно. Компьютерных игроков в список не берём:
// подписки у них не бывает.
func TestPremiumViews(t *testing.T) {
	state := &supremacy.GameState{
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Dau7er", Premium: true},
			29: {ID: 29, Nation: "Франция", Name: "Сосед", Premium: true},
			31: {ID: 31, Nation: "Швеция", Name: "Бедный"},
			33: {ID: 33, Nation: "Бельгия", IsAI: true, Premium: true},
		},
	}

	got := premiumViews(i18n.RU, state, 12)
	if len(got) != 2 {
		t.Fatalf("с премиумом = %+v, ожидались двое живых", got)
	}
	// Порядок по стране: список должен читаться одинаково от захода к заходу.
	if got[0].Nation != "Греция" || !got[0].Mine {
		t.Errorf("первым идёт %+v, ожидалась своя Греция", got[0])
	}
	if got[1].Nation != "Франция" || got[1].Mine {
		t.Errorf("вторым идёт %+v", got[1])
	}
}

// Номер партии приносят по-разному: голым числом, со ссылкой, с мусором
// вокруг. Берём самое длинное число — в адресе игры есть и другие, — а
// короткие обрывки за номер не считаем.
func TestGameIDFromInput(t *testing.T) {
	tests := []struct{ in, want string }{
		{"10892960", "10892960"},
		{"  10892960  ", "10892960"},
		{"https://www.supremacy1914.ru/game.php?gameID=10892960&x=1", "10892960"},
		{"партия 10892960", "10892960"},
		{"", ""},
		{"нет тут номера", ""},
		{"12", ""},
	}
	for _, tt := range tests {
		if got := gameIDFrom(tt.in); got != tt.want {
			t.Errorf("из %q достали %q, ожидалось %q", tt.in, got, tt.want)
		}
	}
}

// Бан игра сообщает про всех участников партии. На карте забаненный
// закрашен своим цветом, но там он только цвет — списком его видно
// в этом отдельном перечне.
func TestBannedViews(t *testing.T) {
	state := &supremacy.GameState{
		Players: map[int]supremacy.Player{
			12: {ID: 12, Nation: "Греция", Name: "Dau7er"},
			29: {ID: 29, Nation: "Франция", Name: "Мультовод", Banned: true},
			31: {ID: 31, Nation: "Швеция", Name: "Ещё один", Banned: true},
		},
	}

	got := bannedViews(i18n.RU, state)
	if len(got) != 2 {
		t.Fatalf("забаненные = %+v, ожидались двое", got)
	}
	if got[0].Nation != "Франция" || got[1].Nation != "Швеция" {
		t.Errorf("порядок = %+v, ожидался по алфавиту", got)
	}
}

// Состав партии приходит вместе с самой партией, и кланы всех её игроков
// берутся оттуда разом. Игрок без клана — это ответ, а не пустота: сайт
// про него сказал, и переспрашивать его не нужно.
func TestAllianceRows(t *testing.T) {
	at := time.Unix(1788984426, 0)
	rows := allianceRows([]supremacy.GameLogin{
		{Login: "Vakyla", SiteUserID: "2953349", AllianceID: "11781", AllianceName: "F L O W"},
		{Login: "MigoV", SiteUserID: "11075095", AllianceID: "0"},
		{Login: "без номера"},
		// Удалённый аккаунт: сайт шлёт ему номер «0». Такой записи быть
		// не должно — иначе все удалённые слипаются в одного игрока.
		{Login: "Deleted User", SiteUserID: "0", AllianceID: "0"},
	}, at)

	if len(rows) != 2 {
		t.Fatalf("записей = %+v, ожидались две: без номера на сайте игрок бесполезен", rows)
	}
	if got := rows[0]; got.ID != "11781" || got.Name != "F L O W" || !got.InClan() {
		t.Errorf("игрок с кланом = %+v", got)
	}
	// Тег состав не отдаёт: он приходит только с составом самого клана.
	if rows[0].Tag != "" {
		t.Errorf("откуда тег? %+v", rows[0])
	}
	if got := rows[1]; got.InClan() || !got.Known() {
		t.Errorf("игрок без клана = %+v: ответ есть, клана нет", got)
	}
	if !rows[0].CheckedAt.Equal(at) {
		t.Errorf("время проверки = %v", rows[0].CheckedAt)
	}
}

// Состав ставится в порядке, в котором его читают: соклановцы рядом,
// безкланные в конце. Внутри клана — по нику, без учёта регистра.
func TestRosterOrder(t *testing.T) {
	views := []rosterView{
		{Login: "один", Clan: ""},
		{Login: "яков", Clan: "F L O W"},
		{Login: "Абрам", Clan: "F L O W"},
		{Login: "второй", Clan: ""},
		{Login: "кто-то", Clan: "VEN.DETTA"},
	}
	sortRoster(views)

	got := []string{}
	for _, v := range views {
		got = append(got, v.Login)
	}
	// Безкланные тоже по нику: список должен читаться, а не лежать
	// в том порядке, в каком его прислал сайт.
	want := []string{"Абрам", "яков", "кто-то", "второй", "один"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок = %v, ожидался %v", got, want)
		}
	}
}

// Режим «Топ кланов» красит только тех, чей клан стоит в верхушке рейтинга,
// и цвет раздаёт по местам: первый номер должен выглядеть одинаково во всех
// партиях, иначе сравнивать соседние партии глазами нечем.
func TestGameMapTopAlliances(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 100, Height: 50,
		Land: map[int][]supremacy.Point{
			1: {{X: 0, Y: 0}, {X: 10, Y: 0}},
			2: {{X: 20, Y: 0}, {X: 30, Y: 0}},
			3: {{X: 40, Y: 0}, {X: 50, Y: 0}},
		},
	}
	state := &supremacy.GameState{
		Players: map[int]supremacy.Player{
			1: {ID: 1, Nation: "Греция"},
			2: {ID: 2, Nation: "Франция"},
			3: {ID: 3, Nation: "Швеция"},
		},
		Provinces: []supremacy.Province{
			{ID: 1, Name: "Афины", Owner: 1},
			{ID: 2, Name: "Париж", Owner: 2},
			{ID: 3, Name: "Стокгольм", Owner: 3},
		},
	}
	sides := &gameSides{
		Alliances: map[int]domain.Alliance{
			// Второе место в рейтинге, первое и третье — не из топа.
			1: {SiteUserID: "1", ID: "127956", Name: "Pride of LIONS", Tag: "*SNG*"},
			2: {SiteUserID: "2", ID: "212184", Name: "Operation Blitzkrieg", Tag: "OP BG"},
			3: {SiteUserID: "3", ID: "843930", Name: "VEN.DETTA", Tag: "-V.D-"},
		},
		Top: map[string]domain.TopAlliance{
			"212184": {ID: "212184", Rank: 1, Name: "Operation Blitzkrieg", Tag: "OP BG"},
			"127956": {ID: "127956", Rank: 2, Name: "Pride of LIONS", Tag: "*SNG*"},
		},
	}

	view := gameMap(i18n.RU, geo, state, sides, 0)

	// Цвет — по месту в рейтинге, а не по владениям и не по порядку
	// игроков в партии: первое место берёт первый цвет палитры.
	first, second, none := view.Shapes[1], view.Shapes[0], view.Shapes[2]
	if !strings.Contains(first.Class, "top") || string(first.TopColor) != mapPalette[0] {
		t.Errorf("первое место: class=%q top=%q", first.Class, first.TopColor)
	}
	if !strings.Contains(second.Class, "top") || string(second.TopColor) != mapPalette[1] {
		t.Errorf("второе место: class=%q top=%q", second.Class, second.TopColor)
	}
	// Клан не из топа в этом режиме ничем не отличается от игрока без клана:
	// пометка означает «из первой десятки», а не «в каком-то клане».
	if strings.Contains(none.Class, "top") || none.TopColor != "" {
		t.Errorf("клан не из топа: class=%q top=%q", none.Class, none.TopColor)
	}
	// Место в рейтинге ещё и словами: по цвету номер не восстановить.
	if !strings.Contains(first.Title, "1 место в рейтинге кланов") {
		t.Errorf("подсказка = %q", first.Title)
	}

	if len(view.Top) != 2 {
		t.Fatalf("легенда топа: %d строк", len(view.Top))
	}
	if view.Top[0].Rank != 1 || view.Top[0].Players != 1 || view.Top[0].Provinces != 1 {
		t.Errorf("первая строка легенды = %+v", view.Top[0])
	}
	if view.Top[1].Name != "Pride of LIONS" || view.Top[1].Tag != "*SNG*" {
		t.Errorf("вторая строка легенды = %+v", view.Top[1])
	}
}

// Без снимка рейтинга режим топа просто пуст: пометка «из топа» без топа
// означала бы, что в партии нет никого известного, а это неправда.
func TestGameMapTopWithoutSnapshot(t *testing.T) {
	geo := &supremacy.MapGeometry{
		Width: 10, Height: 10,
		Land: map[int][]supremacy.Point{1: {{X: 0, Y: 0}, {X: 1, Y: 1}}},
	}
	state := &supremacy.GameState{
		Players:   map[int]supremacy.Player{1: {ID: 1, Nation: "Греция"}},
		Provinces: []supremacy.Province{{ID: 1, Name: "Афины", Owner: 1}},
	}
	sides := &gameSides{Alliances: map[int]domain.Alliance{
		1: {SiteUserID: "1", ID: "212184", Name: "Operation Blitzkrieg"},
	}}

	view := gameMap(i18n.RU, geo, state, sides, 0)
	if len(view.Top) != 0 || strings.Contains(view.Shapes[0].Class, "top") {
		t.Errorf("топ без снимка: легенда %d, class=%q", len(view.Top), view.Shapes[0].Class)
	}
}

// Кого спрашивать у сайта, а кого показать из базы: правило простое —
// незнакомых и тех, чей счёт старше недели. Ошибка здесь стоит дорого
// в обе стороны: лишний номер — это лишний запрос на открытии карты,
// пропущенный — вечно устаревший цвет.
func TestStaleStats(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-6 * 24 * time.Hour)
	old := now.Add(-8 * 24 * time.Hour)
	// Ровно срок — это уже «пора»: иначе запись зависла бы на границе
	// до следующего открытия карты.
	edge := now.Add(-statsFresh)

	known := map[string]domain.UserStats{
		"свежий":     {SiteUserID: "свежий", Level: 12, CheckedAt: &fresh},
		"давний":     {SiteUserID: "давний", Level: 12, CheckedAt: &old},
		"ровно срок": {SiteUserID: "ровно срок", Level: 12, CheckedAt: &edge},
		// Спрошенный, о котором сайт промолчал: уровня нет, но и
		// переспрашивать его до срока незачем.
		"молчун": {SiteUserID: "молчун", CheckedAt: &fresh},
	}
	ids := []string{"свежий", "давний", "ровно срок", "молчун", "новичок", "новичок", ""}

	got := staleStats(known, ids, now)
	want := []string{"давний", "ровно срок", "новичок"}
	if len(got) != len(want) {
		t.Fatalf("спросить собрались %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("на месте %d — %q, ожидалось %q", i, got[i], want[i])
		}
	}
}
