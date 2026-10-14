package supremacy

import (
	"encoding/json"
	"testing"
	"time"
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

// Перед заходом с действием проверяем то, что видно из состояния: герой
// есть и не на карте, лимит не выбран, провинция своя.
func TestHeroDeployCheck(t *testing.T) {
	g := &GameState{
		Me:        6,
		HeroLimit: 2,
		Heroes:    []HeroItem{{Unit: 23041, Item: 23047, Name: "Кангал"}, {Unit: 22277, Item: 22286, Name: "Алленби"}},
		Provinces: []Province{{ID: 481, Name: "Свой", Owner: 6}, {ID: 9, Name: "Чужой", Owner: 2}},
	}
	if hero, why := g.heroDeployCheck(23041, 481); why != nil || hero.Item != 23047 {
		t.Errorf("Кангала в свою провинцию: %+v, %+v", why, hero)
	}
	for _, c := range []struct {
		unit, province int
		name           string
	}{
		{1, 481, "героя нет в инвентаре"},
		{23041, 9, "чужая провинция"},
		{23041, 777, "провинции нет"},
	} {
		if _, why := g.heroDeployCheck(c.unit, c.province); why == nil {
			t.Errorf("%s: проверка пропустила", c.name)
		}
	}

	g.Armies = []Army{{Owner: 6, Units: []ArmyUnit{{Type: 23041, Size: 1}}}}
	if _, why := g.heroDeployCheck(23041, 481); why == nil {
		t.Error("героя на карте разместили второй раз")
	}
	if choices, n := g.HeroChoices(); n != 1 || !choices[0].OnMap {
		t.Errorf("на карте = %d, выбор %+v", n, choices)
	}
	g.HeroLimit = 1
	if _, why := g.heroDeployCheck(22277, 481); why == nil {
		t.Error("лимит героев на карте не сработал")
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
