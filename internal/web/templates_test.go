package web

import (
	"bytes"
	"strings"
	"testing"

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
			},
			// Выбранный фильтр должен пережить перезагрузку страницы.
			want: []string{`value="enemy" selected`, `clan clan-enemy`, "КСГ"},
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
