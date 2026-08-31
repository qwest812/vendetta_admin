package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
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

	tests := []struct {
		page string
		data map[string]any
		want []string
	}{
		{
			page: "home",
			data: map[string]any{
				"Query": "", "Status": "enemy", "Total": 1, "Limit": 50,
				"Players": []*domain.Player{enemy},
				"CanMark": true, "Marked": map[int64]bool{},
			},
			// Выбранный фильтр должен пережить перезагрузку страницы,
			// а пометить врага можно прямо из строки поиска.
			want: []string{
				`value="enemy" selected`, `clan clan-enemy`, "КСГ",
				`action="/enemies/3/mark"`, ">во враги<",
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
			data: map[string]any{"Player": enemy, "Notes": nil, "Error": ""},
			// Клан на карточке — ссылка на клан, а не текст.
			want: []string{`href="/clans/7"`, "Враждебный клан"},
		},
		{
			page: "enemies",
			data: map[string]any{
				"Enemies": []domain.Enemy{{
					Player: enemy, Comment: "слил координаты", CreatedAt: time.Now(),
				}},
				"Error": "", "Candidates": nil, "Marked": map[int64]bool{},
				"Query": "", "Limit": 20, "Comment": "",
			},
			// Комментарий правится на месте, а убрать из списка можно только
			// своей записью — обе формы должны быть на странице.
			want: []string{
				"слил координаты", `action="/enemies/3"`, `action="/enemies/3/delete"`,
			},
		},
	}

	pages, err := parseTemplates()
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			tmpl, ok := pages[tt.page]
			if !ok {
				t.Fatalf("нет шаблона %q", tt.page)
			}
			tt.data["CurrentUser"] = admin
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
		})
	}
}

// Пустой состав клана подписывается иначе, чем пустой поиск: «никого не нашли»
// на карточке клана означало бы не то.
func TestEmptyResultsWording(t *testing.T) {
	pages, err := parseTemplates()
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
	pages, err := parseTemplates()
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
// нельзя было добавить дважды: уже отмеченному кнопки не место.
func TestEnemyCandidates(t *testing.T) {
	pages, err := parseTemplates()
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
			name: "новый игрок",
			data: map[string]any{
				"Candidates": found[:1], "Marked": map[int64]bool{}, "Query": "dau", "Limit": 20,
			},
			want:    []string{"Dau7er", `name="player_id" value="3"`},
			notWant: []string{"уже в списке"},
		},
		{
			name: "уже во врагах",
			data: map[string]any{
				"Candidates": found[:1], "Marked": map[int64]bool{3: true}, "Query": "dau", "Limit": 20,
			},
			want:    []string{"уже в списке"},
			notWant: []string{`name="player_id" value="3"`},
		},
		{
			// Пустой запрос и пустая выдача подписываются по-разному: «никого
			// не нашли» до первого ввода означало бы не то.
			name:    "поиск ещё не начат",
			data:    map[string]any{"Marked": map[int64]bool{}, "Query": "", "Limit": 20},
			want:    []string{"Начните вводить"},
			notWant: []string{"никого не нашли"},
		},
		{
			name:    "нет совпадений",
			data:    map[string]any{"Marked": map[int64]bool{}, "Query": "abc", "Limit": 20},
			want:    []string{"abc", "никого не нашли"},
			notWant: []string{"Начните вводить"},
		},
		{
			name: "выдача упёрлась в предел",
			data: map[string]any{
				"Candidates": found, "Marked": map[int64]bool{}, "Query": "a", "Limit": 2,
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
	pages, err := parseTemplates()
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	tests := []struct {
		name    string
		marked  bool
		want    []string
		notWant []string
	}{
		{
			name:    "ещё не помечен",
			want:    []string{`action="/enemies/3/mark"`, `name="csrf_token"`, ">во враги<"},
			notWant: []string{"во врагах"},
		},
		{
			name:    "уже помечен",
			marked:  true,
			want:    []string{">во врагах<", `href="/enemies"`},
			notWant: []string{"<form", "/enemies/3/mark"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := pages["home"].ExecuteTemplate(&buf, "mark", map[string]any{
				"ID": int64(3), "Marked": tt.marked, "CSRFToken": "csrf",
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
	pages, err := parseTemplates()
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
