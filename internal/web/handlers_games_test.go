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
	if mine.Class != "clan own" || string(mine.ClanColor) != mapPalette[0] {
		t.Errorf("своя провинция: class=%q clan=%q", mine.Class, mine.ClanColor)
	}
	if mine.Points != "0,0 10,0 10,10" {
		t.Errorf("точки = %q", mine.Points)
	}
	if !strings.Contains(mine.Title, "Афины") || !strings.Contains(mine.Title, "Dau7er") ||
		!strings.Contains(mine.Title, "клан VEN.DETTA") {
		t.Errorf("подпись = %q", mine.Title)
	}

	// Игрок, которого нет в базе, остаётся без классов совсем: в любом
	// режиме его закрасит серый по умолчанию.
	if foreign := view.Shapes[1]; foreign.Class != "" || foreign.ClanColor != "" {
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
	// Аккаунт проекта на карте — обычная страна: без карточки в базе он
	// красится общим серым, как и все остальные, а значит без классов.
	if view.Shapes[0].Class != "" {
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

	if got := view.Shapes[1].Class; got != "side-friend" {
		t.Errorf("классы провинции друга = %q", got)
	}
	// Спорная провинция несёт оба класса, а какой победит — решает порядок
	// правил в app.css: там вражда стоит ниже дружбы.
	if got := view.Shapes[2].Class; got != "side-friend side-enemy" {
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
	if shape.Class != "clan team premium own" {
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

// Бан игра сообщает про всех участников партии, и на странице его надо
// показать отдельно: на карте страна забаненного ничем не отличается.
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
