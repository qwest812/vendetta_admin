package supremacy

import (
	"encoding/json"
	"testing"
)

// Витрина отвечает числами в строках и складывает в одну кучу предложения
// по всем героям сразу — форма взята из живого ответа getOffers.
func TestParseHeroOffers(t *testing.T) {
	raw := json.RawMessage(`[
		{"name":"(Recruit) Fiona 'Maeve' Porter - unlock","ii":[50656,50657],"pc":{"50655":"50.0"}},
		{"name":"(Recruit) Fiona 'Maeve' Porter - Promote lvl 2","ii":[50658],"pc":{"50655":"70.0"}},
		{"name":"Что-то без цены","ii":[1],"pc":{}},
		{"name":"Цена не числом","ii":[2],"pc":{"50655":"дорого"}}
	]`)

	got, err := parseHeroOffers(raw)
	if err != nil {
		t.Fatalf("разбор витрины: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("предложений %d, ждали 2: %+v", len(got), got)
	}
	if got[0].Shards[50655] != 50 || got[1].Shards[50655] != 70 {
		t.Errorf("цены разобрались не так: %+v", got)
	}
	if len(got[0].Items) != 2 || got[0].Items[1] != 50657 {
		t.Errorf("выдаваемые предметы: %+v", got[0].Items)
	}
}

// Пустая витрина — не ошибка: так отвечает аккаунт, которому героев
// не показывают.
func TestParseHeroOffersEmpty(t *testing.T) {
	got, err := parseHeroOffers(json.RawMessage(`[]`))
	if err != nil || len(got) != 0 {
		t.Fatalf("пустая витрина: %+v, %v", got, err)
	}
}
