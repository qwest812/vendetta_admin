package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Шаблоны — единственная часть админки, где ошибка вылезает только в рантайме
// и только на живой странице. Поэтому каждую страницу прогоняем на правдоподобных
// данных и проверяем то, ради чего она есть.
func TestPagesRender(t *testing.T) {
	admin := &domain.User{ID: 1, Email: "admin@example.com", Role: domain.RoleAdmin}
	clanID := int64(7)
	enemy := &domain.Player{
		ID: 3, Nickname: "Dau7er", GameID: "42",
		ClanID: &clanID, ClanName: "КСГ", ClanStatus: domain.ClanEnemy,
	}
	clan := domain.Clan{ID: clanID, Name: "КСГ", Status: domain.ClanEnemy, Players: 1}

	// Часть страниц выглядит по-разному для разных ролей, поэтому у случая
	// есть свой смотрящий; пусто — обычный админ.
	banSeen := time.Unix(1788984426, 0)
	root := &domain.User{ID: 1, Nickname: "root", Role: domain.RoleRoot}
	player := &domain.User{ID: 2, Nickname: "Dau7er", Role: domain.RoleUser,
		GamesAccess: true, GameID: "101408369"}

	tests := []struct {
		name string
		page string
		user *domain.User
		data map[string]any
		want []string
		deny []string // чего на странице быть не должно
	}{
		{
			page: "home",
			data: map[string]any{
				"Query": "", "Status": "enemy", "Total": 1, "Limit": 50,
				"Players": []*domain.Player{enemy},
				"CanMark": true, "MarkedEnemies": map[int64]bool{},
				"MarkedFriends": map[int64]bool{},
				"EnemyWords":    enemyWords, "FriendWords": friendWords,
			},
			// Выбранный фильтр должен пережить перезагрузку страницы,
			// а пометить врага можно прямо из строки поиска.
			want: []string{
				`value="enemy" selected`, `clan clan-enemy`, "КСГ",
				// Из строки поиска игрок помечается в оба личных списка.
				`action="/enemies/3/mark"`, ">во враги<",
				`action="/friends/3/mark"`, ">в друзья<",
			},
		},
		{
			page: "profile",
			data: map[string]any{
				"Profile": &domain.User{
					ID: 1, Nickname: "root", Email: "admin@example.com", Role: domain.RoleAdmin,
					FullName: "Ярослав", City: "Киев", GameID: "101408369",
				},
				"Error": "", "Notice": "",
			},
			// Заполненное должно возвращаться в поля, иначе правка стирает
			// то, что уже было записано.
			want: []string{
				`action="/profile"`, `name="full_name" value="Ярослав"`,
				`name="city" value="Киев"`, `name="game_id" value="101408369"`,
			},
		},
		{
			page: "clans",
			data: map[string]any{
				"Clans": []domain.Clan{clan}, "Statuses": domain.ClanStatuses, "Error": "",
			},
			want: []string{`href="/clans/7"`, `value="enemy" selected`, "Враждебный"},
		},
		{
			page: "clan",
			data: map[string]any{
				"Clan": clan, "Statuses": domain.ClanStatuses,
				"Players": []*domain.Player{enemy}, "Limit": 200,
			},
			want: []string{"badge clan-enemy", "Враждебный", "Dau7er"},
		},
		{
			page: "player",
			data: map[string]any{"Player": enemy, "Notes": nil, "Error": "", "Seen": nil},
			// Клан на карточке — ссылка на клан, а не текст.
			want: []string{`href="/clans/7"`, "Враждебный клан"},
			// Про игру мы ничего не знаем — и молчим об этом.
			deny: []string{"В игре", "забанен"},
		},
		{
			// Бан игра рассказывает про аккаунт, поэтому он и живёт
			// в карточке, а не только в партии, где мы его увидели.
			name: "player/забанен в игре",
			page: "player",
			data: map[string]any{
				"Player": enemy, "Notes": nil, "Error": "",
				"Seen": &domain.GamePlayer{
					SiteUserID: "777", Nickname: "Мультовод", Banned: true,
					BannedAt: &banSeen, SeenAt: banSeen, SeenGameID: "10892960",
				},
			},
			want: []string{
				"забанен в игре", "под ником Мультовод",
				`href="/games/10892960"`,
			},
		},
		{
			name: "games/root",
			page: "games",
			user: root,
			data: map[string]any{
				"Games": []gameView{{
					ID: "10867066", Title: "[Speed] - The Great War", State: "идёт",
					Day: "6", Players: "31", Language: "ru", PlayerID: "32",
					Started: time.Unix(1785920128, 0), Joined: time.Unix(1785920263, 0),
				}},
				"Error": "", "PlayURL": "https://www.supremacy1914.com/game.php?bust=1&uid=101408369",
				"CheckError": "", "Query": "",
			},
			// Игру надо узнать в списке, суметь открыть в клиенте и проверить
			// чужую партию по номеру.
			want: []string{
				"[Speed] - The Great War", "ID 10867066", "идёт",
				"game.php?bust=1&amp;uid=101408369",
				`action="/games/check"`, "Проверить игру",
			},
		},
		{
			// Список партий говорит, где аккаунт играет прямо сейчас, — это
			// рутовое знание. Остальным остаётся проверка по номеру.
			name: "games/доступ без рута",
			page: "games",
			user: player,
			data: map[string]any{
				"Games": nil, "Error": "", "PlayURL": "",
				"CheckError": "", "Query": "",
			},
			want: []string{`action="/games/check"`, "Проверить игру", `name="id"`},
			deny: []string{"Активные игры", "Обновить"},
		},
		{
			name: "game/root",
			page: "game",
			user: root,
			data: map[string]any{
				"GameID": "10886819",
				"Game": &gameView{
					ID: "10886819", Title: "[Speed] - The Great War", State: "идёт",
					Day: "13", Players: "31", PlayerID: "12",
				},
				"Task": &domain.GameTask{
					GameID: "10886819", HeroDeploy: true,
					LastRunAt: time.Now(), LastResult: "пехота призвана",
				},
				"Interval": 45 * time.Minute, "Error": "", "Ours": true,
				"State": nil, "Mine": nil,
			},
			// Переключатель показывает обратное действие, обещанный интервал
			// пишется словами, а итог прошлого захода виден на странице.
			want: []string{
				`action="/games/10886819/hero"`, `name="on" value="0"`,
				"Выключить призыв пехоты", "каждые 45 мин", "пехота призвана",
				// Ручной призыв — отдельная форма рядом с переключателем.
				`action="/games/10886819/hero/run"`, "Призвать сейчас",
			},
		},
		{
			// Смотрящий с доступом видит партию своими глазами: страна берётся
			// из его профиля, а кнопки призыва остаются рутовыми.
			name: "game/своя страна",
			page: "game",
			user: player,
			data: map[string]any{
				"GameID": "10886819",
				"Game": &gameView{
					ID: "10886819", Title: "[Speed] - The Great War", State: "идёт",
					Day: "13", Players: "31", PlayerID: "12",
				},
				"Task":     &domain.GameTask{GameID: "10886819", HeroDeploy: true},
				"Interval": 45 * time.Minute, "Error": "", "Ours": true,
				"State": &supremacy.GameState{Day: 13, Me: 12,
					Provinces: []supremacy.Province{{ID: 1}, {ID: 2}}},
				"Me":   &supremacy.Player{ID: 29, Nation: "Франция", Name: "Dau7er"},
				"Mine": []supremacy.Province{{Name: "Париж", Capital: true, Morale: 88}},
			},
			want: []string{"вы играете за Франция", "номер в партии 29", "Париж", "столица"},
			deny: []string{"Призвать сейчас", "призыв пехоты", "аккаунт проекта"},
		},
		{
			// Карта без легенды бесполезна: цвет раздаётся кланам на партию,
			// и понять, чей он, можно только по подписи под картой.
			name: "game/легенда карты",
			page: "game",
			user: player,
			data: map[string]any{
				"GameID":   "10886819",
				"Game":     &gameView{ID: "10886819", Title: "Партия", State: "идёт"},
				"Interval": 45 * time.Minute, "Ours": true,
				"State": &supremacy.GameState{Day: 13},
				"Map": &gameMapView{
					Width: 100, Height: 50,
					Shapes: []mapShape{
						{Points: "0,0 10,0 10,10", Class: "clan side-enemy team premium own",
							ClanColor: template.CSS(mapPalette[0]),
							TeamColor: template.CSS("rgb(120,90,120)"), Title: "Афины"},
						{Points: "20,0 30,0 30,10", Class: "neutral", Title: "Ничьё"},
					},
					Labels: []mapLabel{
						{X: 5, Y: 5, Text: "Греция", Nick: "Dau7er"},
						{X: 25, Y: 5, Text: "Швеция"},
					},
					Legend: []allianceLegendView{
						{ID: "843930", Name: "VEN.DETTA", Tag: "-V.D-", Color: mapPalette[0], Provinces: 12},
						{ID: "77", Name: "Мелкий", Provinces: 1},
					},
					Teams: []teamLegendView{
						{Name: "КСГ", Color: "rgb(120,90,120)", Members: 4, Mine: true},
					},
				},
				"Enemies": []gameRelationView{{
					Nation: "Франция", Name: "Враг", Premium: true,
					Card: &domain.Player{ID: 3, Nickname: "Враг"},
				}},
				"Premium": []gamePlayerView{
					{Nation: "Франция", Name: "Враг"},
					{Nation: "Швеция", Name: "Сосед"},
				},
				"Banned":     []gamePlayerView{{Nation: "Швеция", Name: "Сосед"}},
				"NotPlaying": true,
			},
			want: []string{
				// Обе кнопки режимов и обе легенды на странице: переключает
				// их css, поэтому в разметке они есть всегда.
				`id="map-clans"`, `id="map-sides"`, `id="map-teams"`, `id="map-premium"`,
				"Кланы", "Мои списки", "Коалиции", "Премиум",
				"Во врагах", "В друзьях",
				// Коалиция — из игры: своё название, свой цвет, и своя
				// помечается отдельно.
				"КСГ", "rgb(120,90,120)", "ваша",
				// Премиум виден и списком по партии, и меткой у врага
				// в личном списке.
				"Премиум («Высокое командование») в этой партии", "премиум", "Швеция",
				"Забанены:",
				// Цвета едут переменными: нужную из них берёт css того
				// режима, который выбран.
				"--clan: " + mapPalette[0], "--team: rgb(120,90,120)",
				`class="clan side-enemy team premium own"`, `class="neutral"`,
				// Ник — вторая строка подписи: у svg переноса нет, поэтому
				// строка заново задаёт x и сдвиг.
				`<tspan class="player" x="5" dy="36">Dau7er</tspan>`,
				"VEN.DETTA", "-V.D-",
				// Клан без цвета остаётся в легенде: на карте он серый,
				// и узнать его больше неоткуда.
				"Мелкий", "#4a5b68",
			},
		},
		{
			// Без игрового ID в профиле своей страны не найти — и об этом
			// говорится прямо, со ссылкой, куда его вписать.
			name: "game/без игрового ID",
			page: "game",
			user: &domain.User{ID: 3, Nickname: "Новичок", Role: domain.RoleUser, GamesAccess: true},
			data: map[string]any{
				"GameID":   "10886819",
				"Game":     &gameView{ID: "10886819", Title: "Партия", State: "идёт"},
				"Interval": 45 * time.Minute, "Ours": true,
				"State":           &supremacy.GameState{Day: 13},
				"NoProfileGameID": true,
			},
			want: []string{`href="/profile"`, "укажите игровой ID"},
			deny: []string{"вы играете за", "Призвать сейчас"},
		},
		{
			name: "game/не наша партия",
			page: "game",
			user: player,
			data: map[string]any{
				"GameID": "10895766",
				"Game": &gameView{
					ID: "10895766", Title: "[Event] - Colonial Uprising",
					State: "набор", Day: "1", Players: "97", Language: "ru",
				},
				"Interval": 45 * time.Minute, "Ours": false,
				"Roster": []rosterView{
					{Login: "Vakyla", Clan: "F L O W", Team: "5", Level: 17,
						Card: &domain.Player{ID: 3, Nickname: "Враг"}, Enemy: true},
					{Login: "MigoV", Level: 13},
				},
			},
			// Посмотреть со стороны не вышло — остаются сведения из лобби
			// и состав с сайта. Кнопки захода у чужой партии нет: заходить
			// в неё нечем, а наблюдателем страница смотрит сама.
			want: []string{
				"[Event] - Colonial Uprising", "набор", "97",
				"общий аккаунт проекта не играет",
				"Кто играет", "Vakyla", "F L O W", "№5", "17",
				// Кого знаем — тот со ссылкой на карточку и с меткой списка.
				`href="/players/3"`, "во врагах",
				"MigoV",
			},
			deny: []string{"Заглянуть в партию", "Что у нас в партии", "Призвать сейчас"},
		},
		{
			// Чужая партия глазами наблюдателя: карта с коалициями есть,
			// а делать в партии нечего — кнопок призыва тут быть не должно,
			// как и кнопки захода: страница уже посмотрела всё сама.
			name: "game/чужая партия со стороны",
			page: "game",
			user: player,
			data: map[string]any{
				"GameID": "10896278",
				"Game": &gameView{
					ID: "10896278", Title: "[Speed] - The Great War",
					State: "идёт", Day: "1", Players: "45",
				},
				"Interval": 45 * time.Minute, "Ours": false,
				"State":      &supremacy.GameState{Day: 1},
				"NotPlaying": true,
				"Map": &gameMapView{
					Width: 100, Height: 50,
					Shapes: []mapShape{{Points: "0,0 10,0 10,10", Class: "team",
						TeamColor: template.CSS("rgb(120,140,40)"), Title: "Москва"}},
					Teams: []teamLegendView{
						{Name: "Союз независимых", Color: "rgb(120,140,40)", Members: 2},
					},
				},
			},
			want: []string{
				"Что в партии", "общий аккаунт проекта не играет",
				"входом в неё он не считается",
				"Коалиции", "Союз независимых",
				"В этой партии вы не играете",
			},
			deny: []string{
				"Заглянуть в партию", "Что у нас в партии", "Призвать сейчас",
				// Состав с сайта здесь лишний: те же люди видны на карте.
				"Кто играет",
			},
		},
		{
			name: "game/чужая партия",
			page: "game",
			user: player,
			data: map[string]any{
				"GameID":   "10886819",
				"Game":     &gameView{ID: "10886819", Title: "Партия", State: "идёт"},
				"Interval": 45 * time.Minute, "Ours": true,
				"State":      &supremacy.GameState{Day: 13},
				"NotPlaying": true,
			},
			want: []string{"В этой партии вы не играете"},
			deny: []string{"вы играете за", "Призвать сейчас"},
		},
		{
			page: "enemies",
			data: map[string]any{
				"Words": enemyWords,
				"List": []domain.Relation{{
					Player: enemy, Comment: "слил координаты", CreatedAt: time.Now(),
				}},
				"Error": "", "Candidates": nil, "Marked": map[int64]bool{},
				"Query": "", "Limit": 20, "Comment": "",
			},
			// Комментарий правится на месте, а убрать из списка можно только
			// своей записью — обе формы должны быть на странице.
			want: []string{
				"слил координаты", `action="/enemies/3"`, `action="/enemies/3/delete"`,
				"Мои враги",
			},
			deny: []string{"/friends/"},
		},
		{
			// Друзья — та же разметка с другими словами: пути и заголовок
			// должны смениться целиком, иначе кнопка уведёт не туда.
			page: "friends",
			data: map[string]any{
				"Words": friendWords,
				"List": []domain.Relation{{
					Player: enemy, Comment: "вместе держали фронт", CreatedAt: time.Now(),
				}},
				"Error": "", "Candidates": nil, "Marked": map[int64]bool{},
				"Query": "", "Limit": 20, "Comment": "",
			},
			want: []string{
				"вместе держали фронт", `action="/friends/3"`, `action="/friends/3/delete"`,
				"Мои друзья",
			},
			deny: []string{"/enemies/"},
		},
	}

	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	for _, tt := range tests {
		name := tt.name
		if name == "" {
			name = tt.page
		}
		t.Run(name, func(t *testing.T) {
			tmpl, ok := pages[tt.page]
			if !ok {
				t.Fatalf("нет шаблона %q", tt.page)
			}
			user := tt.user
			if user == nil {
				user = admin
			}
			tt.data["CurrentUser"] = user
			tt.data["CSRFToken"] = "csrf"
			tt.data["Path"] = "/"

			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "base.gohtml", tt.data); err != nil {
				t.Fatalf("отрисовка: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("на странице нет %q", want)
				}
			}
			for _, deny := range tt.deny {
				if strings.Contains(buf.String(), deny) {
					t.Errorf("на странице есть лишнее %q", deny)
				}
			}
		})
	}
}

// Пустой состав клана подписывается иначе, чем пустой поиск: «никого не нашли»
// на карточке клана означало бы не то.
func TestEmptyResultsWording(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	tests := []struct {
		name string
		data map[string]any
		want string
	}{
		{"пустой поиск", map[string]any{"Limit": 50}, "В базе пока нет игроков"},
		{"нет совпадений", map[string]any{"Query": "abc", "Limit": 50}, "abc"},
		{"фильтр по статусу", map[string]any{"Status": "ally", "Limit": 50}, "С таким статусом клана никого нет"},
		{"пустой клан", map[string]any{"Empty": "В этом клане пока нет карточек.", "Limit": 200}, "В этом клане пока нет карточек."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := pages["home"].ExecuteTemplate(&buf, "results", tt.data); err != nil {
				t.Fatalf("отрисовка: %v", err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Errorf("ожидалось %q, получено: %s", tt.want, buf.String())
			}
		})
	}
}

// Пользователю без прав админа кланы показываются, но не правятся: статус —
// это политика, её ставит админ.
func TestClansPageIsReadOnlyForUser(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	var buf bytes.Buffer
	err = pages["clans"].ExecuteTemplate(&buf, "base.gohtml", map[string]any{
		"CurrentUser": &domain.User{ID: 2, Nickname: "user", Role: domain.RoleUser},
		"CSRFToken":   "csrf",
		"Path":        "/clans",
		"Clans":       []domain.Clan{{ID: 7, Name: "КСГ", Status: domain.ClanEnemy, Players: 3}},
		"Statuses":    domain.ClanStatuses,
	})
	if err != nil {
		t.Fatalf("отрисовка: %v", err)
	}

	page := buf.String()
	if !strings.Contains(page, "badge clan-enemy") {
		t.Error("статус клана должен быть виден и без прав админа")
	}
	for _, forbidden := range []string{"<select", "<form class=\"row-form\"", "/clans/7/delete"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("на странице есть %q — правка должна быть только у админа", forbidden)
		}
	}
}

// Подбор в форме добавления отвечает за то, чтобы одного и того же игрока
// нельзя было добавить дважды, и чтобы без запроса он ничего не предлагал:
// иначе свежая карточка стоит первой строкой и её принимают за врага.
func TestEnemyCandidates(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	found := []*domain.Player{
		{ID: 3, Nickname: "Dau7er", GameID: "42"},
		{ID: 4, Nickname: "Sever", GameID: "43"},
	}

	tests := []struct {
		name    string
		data    map[string]any
		want    []string
		notWant []string
	}{
		{
			name: "нашли одного",
			data: map[string]any{
				"Words":      enemyWords,
				"Candidates": found[:1], "Marked": map[int64]bool{},
				"Query": "dau", "Limit": 20, "CanAdd": true,
			},
			want:    []string{"Dau7er", `<option value="3"`, ">Добавить<"},
			notWant: []string{"уже в списке", "disabled"},
		},
		{
			// Уже добавленного из списка не убираем, но выбрать не даём:
			// иначе кажется, что поиск его не нашёл.
			name: "единственный найденный уже во врагах",
			data: map[string]any{
				"Words":      enemyWords,
				"Candidates": found[:1], "Marked": map[int64]bool{3: true},
				"Query": "dau", "Limit": 20, "CanAdd": false,
			},
			want:    []string{"уже в списке", "disabled", "Все найденные уже у вас во врагах"},
			notWant: []string{">Добавить<"},
		},
		{
			name: "часть найденных уже во врагах",
			data: map[string]any{
				"Words":      friendWords,
				"Candidates": found, "Marked": map[int64]bool{3: true},
				"Query": "a", "Limit": 20, "CanAdd": true,
			},
			want: []string{"уже в списке", "disabled", ">Добавить<", `<option value="4"`},
		},
		{
			// Игровой ID вписывают руками, и у старых карточек его нет —
			// в списке это должно быть видно, а не выглядеть пропущенным полем.
			name: "карточка без игрового ID",
			data: map[string]any{
				"Candidates": []*domain.Player{{ID: 5, Nickname: "Безымянный"}},
				"Marked":     map[int64]bool{}, "Query": "без", "Limit": 20, "CanAdd": true,
				"Words": enemyWords,
			},
			want: []string{"Безымянный", "без ID"},
		},
		{
			// Пока ничего не набрано, предлагать некого: подбор — ответ
			// на запрос, а не витрина базы.
			name: "без запроса",
			data: map[string]any{"Words": enemyWords, "Marked": map[int64]bool{}, "Query": "", "Limit": 20},
			want: []string{"Наберите ник или игровой ID"},
			// Ни списка, ни жалобы на пустую выдачу: человек ещё не искал.
			notWant: []string{"<option", "никого не нашли"},
		},
		{
			name:    "нет совпадений",
			data:    map[string]any{"Words": enemyWords, "Marked": map[int64]bool{}, "Query": "abc", "Limit": 20},
			want:    []string{"abc", "никого не нашли"},
			notWant: []string{"<option"},
		},
		{
			name: "выдача упёрлась в предел",
			data: map[string]any{
				"Words":      enemyWords,
				"Candidates": found, "Marked": map[int64]bool{},
				"Query": "a", "Limit": 2, "CanAdd": true,
			},
			want: []string{"уточните запрос"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := pages["enemies"].ExecuteTemplate(&buf, "candidates", tt.data); err != nil {
				t.Fatalf("отрисовка: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("в подборе нет %q: %s", want, buf.String())
				}
			}
			for _, forbidden := range tt.notWant {
				if strings.Contains(buf.String(), forbidden) {
					t.Errorf("в подборе есть лишнее %q: %s", forbidden, buf.String())
				}
			}
		})
	}
}

// Кнопка «во враги» в строке поиска. Уже помеченному кнопки не место: нажать
// второй раз нечего, зато нужен путь к списку, где пишется комментарий.
func TestSearchRowMark(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	tests := []struct {
		name    string
		marked  bool
		words   relationWords
		want    []string
		notWant []string
	}{
		{
			name:    "ещё не помечен",
			words:   enemyWords,
			want:    []string{`action="/enemies/3/mark"`, `name="csrf_token"`, ">во враги<"},
			notWant: []string{"во врагах"},
		},
		{
			name:    "уже помечен",
			words:   enemyWords,
			marked:  true,
			want:    []string{">во врагах<", `href="/enemies"`},
			notWant: []string{"<form", "/enemies/3/mark"},
		},
		{
			// Та же пометка с другими словами ведёт в другой раздел.
			name:    "друзья: ещё не помечен",
			words:   friendWords,
			want:    []string{`action="/friends/3/mark"`, ">в друзья<", "mark friend"},
			notWant: []string{"/enemies/"},
		},
		{
			name:    "друзья: уже помечен",
			words:   friendWords,
			marked:  true,
			want:    []string{">в друзьях<", `href="/friends"`},
			notWant: []string{"<form", "/enemies/"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := pages["home"].ExecuteTemplate(&buf, "mark", map[string]any{
				"ID": int64(3), "Marked": tt.marked, "CSRFToken": "csrf", "Words": tt.words,
			})
			if err != nil {
				t.Fatalf("отрисовка: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("в пометке нет %q: %s", want, buf.String())
				}
			}
			for _, forbidden := range tt.notWant {
				if strings.Contains(buf.String(), forbidden) {
					t.Errorf("в пометке есть лишнее %q: %s", forbidden, buf.String())
				}
			}
		})
	}
}

// Состав клана — тот же список строк, но без пометок: список врагов личный,
// а состав клана смотрят как справку. Данных для кнопки там нет, и шаблон
// не должен на этом падать.
func TestClanRosterHasNoMark(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	var buf bytes.Buffer
	err = pages["clan"].ExecuteTemplate(&buf, "results", map[string]any{
		"Players": []*domain.Player{{ID: 3, Nickname: "Dau7er"}}, "Limit": 200,
	})
	if err != nil {
		t.Fatalf("отрисовка: %v", err)
	}
	if !strings.Contains(buf.String(), "Dau7er") {
		t.Error("состав клана должен показывать игроков")
	}
	if strings.Contains(buf.String(), "/mark") {
		t.Error("в составе клана пометки «во враги» быть не должно")
	}
}

// Раздел «Игры» — про общий игровой аккаунт проекта, поэтому в меню он
// только у рута: у админа прав на него нет, а ссылка ведёт в 403.
func TestGamesLinkIsRootOnly(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	render := func(u *domain.User) string {
		var buf bytes.Buffer
		err := pages["home"].ExecuteTemplate(&buf, "base.gohtml", map[string]any{
			"CurrentUser": u, "CSRFToken": "csrf", "Path": "/",
			"Query": "", "Status": "", "Total": 0, "Limit": 50,
			"Players": nil, "CanMark": true,
			"MarkedEnemies": map[int64]bool{}, "MarkedFriends": map[int64]bool{},
			"EnemyWords": enemyWords, "FriendWords": friendWords,
		})
		if err != nil {
			t.Fatalf("отрисовка: %v", err)
		}
		return buf.String()
	}

	if page := render(&domain.User{ID: 1, Nickname: "root", Role: domain.RoleRoot}); !strings.Contains(page, `href="/games"`) {
		t.Error("у рута в меню должен быть раздел «Игры»")
	}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleUser} {
		page := render(&domain.User{ID: 2, Nickname: "someone", Role: role})
		if strings.Contains(page, `href="/games"`) {
			t.Errorf("роль %s не должна видеть раздел «Игры»", role)
		}
	}
}

// Аккаунт игры может быть не настроен или не ответить: страница обязана
// объяснить это словами, а не показать пустую таблицу как «игр нет».
func TestGamesPageExplainsFailure(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	var buf bytes.Buffer
	err = pages["games"].ExecuteTemplate(&buf, "base.gohtml", map[string]any{
		"CurrentUser": &domain.User{ID: 1, Nickname: "root", Role: domain.RoleRoot},
		"CSRFToken":   "csrf", "Path": "/games",
		"Games": nil, "Error": "Аккаунт Supremacy 1914 не настроен", "PlayURL": "",
	})
	if err != nil {
		t.Fatalf("отрисовка: %v", err)
	}

	page := buf.String()
	if !strings.Contains(page, "Аккаунт Supremacy 1914 не настроен") {
		t.Error("причина отказа должна быть на странице")
	}
	if strings.Contains(page, "Сейчас аккаунт не играет") {
		t.Error("после ошибки нельзя утверждать, что игр нет")
	}
}

// Состав партии стоит захода на игровой сервер, поэтому по умолчанию его
// на странице нет — вместо него объяснение и кнопка.
func TestGamePageAsksBeforeEnteringGame(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	var buf bytes.Buffer
	err = pages["game"].ExecuteTemplate(&buf, "base.gohtml", map[string]any{
		"CurrentUser": &domain.User{ID: 1, Nickname: "root", Role: domain.RoleRoot},
		"CSRFToken":   "csrf", "Path": "/games/10886819",
		"GameID":   "10886819",
		"Game":     &gameView{ID: "10886819", Title: "The Great War", State: "идёт"},
		"Task":     &domain.GameTask{GameID: "10886819"},
		"Interval": 45 * time.Minute, "Error": "", "Ours": true, "State": nil, "Mine": nil,
	})
	if err != nil {
		t.Fatalf("отрисовка: %v", err)
	}

	page := buf.String()
	if !strings.Contains(page, `href="/games/10886819?state=1"`) {
		t.Error("на странице должна быть кнопка захода в партию")
	}
	if !strings.Contains(page, "Включить призыв пехоты") {
		t.Error("выключенный автопризыв должен предлагать включение")
	}
	if strings.Contains(page, "Провинция") {
		t.Error("таблица провинций не должна появляться без захода в партию")
	}
}

// Игровой ID обязателен и в форме правки: без него карточку не опознать
// после смены ника, а старые записи иначе так и остались бы без ID.
func TestPlayerFormRequiresGameID(t *testing.T) {
	pages, err := parseTemplates(i18n.RU)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	for _, tt := range []struct {
		name   string
		player *domain.Player
	}{
		{"новая карточка", nil},
		{"правка", &domain.Player{ID: 3, Nickname: "Dau7er"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			data := map[string]any{
				"CurrentUser": &domain.User{ID: 1, Nickname: "admin", Role: domain.RoleAdmin},
				"CSRFToken":   "csrf", "Path": "/players/new",
				"Player": tt.player, "Traits": nil, "Clans": nil,
				"Selected": map[int64]bool{}, "Error": "",
			}
			if tt.player == nil {
				data["Player"] = nil
			}
			if err := pages["player_form"].ExecuteTemplate(&buf, "base.gohtml", data); err != nil {
				t.Fatalf("отрисовка: %v", err)
			}
			page := buf.String()
			if !strings.Contains(page, `name="game_id"`) || !strings.Contains(page, "required") {
				t.Error("поле игрового ID должно быть обязательным")
			}
			if strings.Contains(page, "(можно оставить пустым)") {
				t.Error("подсказка о необязательности ID устарела")
			}
		})
	}
}
