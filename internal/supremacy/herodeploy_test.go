package supremacy

import (
	"encoding/json"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

// Герои аккаунта собираются из трёх мест состояния, как их снял живой
// клиент: справочник «юнит → предмет», названия предметов и инвентарь.
// Предмета нет в инвентаре — героя у аккаунта нет.
func TestHeroesFromState(t *testing.T) {
	const doc = `{"states": {
	  "3": {"map": {"locations": [{"@c": "p", "id": 481, "o": 6}]}},
	  "11": {"unitItemIdToPermanentPremiumItemId": {"23041": {"identifier": 23047}, "22277": {"identifier": 22286}},
	         "premiums": {"23047": {"name": "Орхан «Кангал» Демир"}, "22286": {"name": "Виконт Алленби"}}},
	  "16": {"inventory": {"23047": {"itemID": 23047, "itemAmount": 1}}},
	  "31": {"heroConfig": {"limit": 5}}
	}}`
	var resp gameStateResponse
	if err := json.Unmarshal([]byte(doc), &resp); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	g, err := resp.build("10927352", 6)
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}
	want := HeroItem{Unit: 23041, Item: 23047, Name: "Орхан «Кангал» Демир"}
	if len(g.Heroes) != 1 || g.Heroes[0] != want {
		t.Errorf("герои = %+v, ожидался %+v", g.Heroes, want)
	}
	if g.HeroLimit != 5 {
		t.Errorf("лимит = %d", g.HeroLimit)
	}
}

// Приказы разбираются по порядку и по условиям: день, лимит героев,
// герой на карте, своя провинция, её мораль и бой в ней. Место в лимите
// достаётся первому; кого не поставить сейчас — ждут с причиной и сроком.
func TestPlanHeroOrders(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	g := &GameState{
		Me: 6, Day: 5, NextDay: now.Add(time.Hour), DayLength: 24 * time.Hour, HeroLimit: 2,
		Heroes: []HeroItem{
			{Unit: 1, Item: 11, Name: "Первый"}, {Unit: 2, Item: 12, Name: "Второй"},
			{Unit: 3, Item: 13, Name: "Третий"}, {Unit: 4, Item: 14, Name: "Поздний"},
			{Unit: 5, Item: 15, Name: "На карте"},
		},
		Provinces: []Province{
			{ID: 100, Owner: 6, Morale: 90}, {ID: 200, Owner: 6, Morale: 40},
			{ID: 300, Owner: 6, Morale: 90}, {ID: 400, Owner: 9, Morale: 90},
		},
		Armies: []Army{
			{Owner: 6, Units: []ArmyUnit{{Type: 5}}},
			{Owner: 9, Location: 300, Fighting: true},
		},
	}
	cards := map[int]HeroCard{11: {Day: 1, Morale: 70}, 12: {Day: 1, Morale: 70},
		13: {Day: 1, Morale: 70}, 14: {Day: 8, Morale: 70}}
	orders := []domain.HeroPlan{
		{ID: 1, Unit: 4, Province: 100, City: "Город"},  // ждёт 8-го дня
		{ID: 2, Unit: 2, Province: 200, City: "Слабый"}, // мораль 40 из 70
		{ID: 3, Unit: 1, Province: 300, City: "Бой"},    // бой в провинции
		{ID: 4, Unit: 3, Province: 100, City: "Город"},  // встанет: место одно
		{ID: 5, Unit: 1, Province: 100, City: "Город"},  // места уже нет
		{ID: 6, Unit: 5, Province: 100, City: "Город"},  // уже на карте
		{ID: 7, Unit: 2, Province: 400, City: "Чужой"},  // чужая провинция
		{ID: 8, Unit: 9, Province: 100, City: "Город"},  // нет в инвентаре
	}
	steps := planHeroOrders(g, cards, orders, now)

	if !steps[3].place || steps[3].hero.Item != 13 {
		t.Errorf("приказ 4 не ставится: %+v", steps[3])
	}
	for i, s := range steps {
		if i != 3 && s.place {
			t.Errorf("приказ %d ставится, а не должен: %+v", s.order.ID, s.run)
		}
	}
	if want := g.DayStart(8).Add(2 * time.Minute); !steps[0].run.Next.Equal(want) {
		t.Errorf("приказ 1 ждёт до %v, ожидалось начало 8-го дня %v", steps[0].run.Next, want)
	}
	for _, i := range []int{1, 2, 4} {
		if !steps[i].run.Next.Equal(now.Add(heroRetry)) {
			t.Errorf("приказ %d: %q, следующий заход %v — ожидался через час", i+1, steps[i].run.Result, steps[i].run.Next)
		}
	}
	for _, i := range []int{5, 6, 7} {
		if !steps[i].run.Next.IsZero() {
			t.Errorf("приказ %d не закрыт: %q", i+1, steps[i].run.Result)
		}
	}
}

// Справочник предметов сайта: описание предмета — JSON строкой внутри
// JSON, номер предмета — строкой. Предметы без карточки юнита пропускаем.
func TestParseHeroCards(t *testing.T) {
	const raw = `[
	  {"@c": "ultshared.UltMod", "itemID": "1"},
	  {"itemID": "23047", "name": "Orhan 'Kangal' Demir",
	   "extraSettings": "{\"inventorySystemItem\":{\"itemType\":\"unit\",\"unitCardContent\":{\"mobilizationTime\":7200000,\"deploymentMoraleThreshold\":70,\"dayOfAvailability\":2}}}"},
	  {"itemID": "5", "extraSettings": "{\"hidden\":true}"}
	]`
	cards, err := parseHeroCards([]byte(raw))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	want := HeroCard{Day: 2, Morale: 70, Mobilization: 2 * time.Hour}
	if len(cards) != 1 || cards[23047] != want {
		t.Errorf("карточки = %+v, ожидалась %+v", cards, want)
	}
}
