package supremacy

import (
	"encoding/json"
	"testing"
)

// Формы взяты с живого игрового сервера: у суши «p» есть владелец «o»,
// у моря «sp» его нет вовсе, а имена и мораль приходят в «n» и «m».
const gameStateFixture = `{
 "timeStamp": "1788984426329",
 "states": {
  "1": {"players": {
    "-1": {"playerID": -1, "nationName": "", "userName": ""},
    "12": {"playerID": 12, "nationName": "Греция", "userName": "Dau7er", "capitalID": 430},
    "29": {"playerID": 29, "nationName": "Франция", "userName": "AI", "computerPlayer": true, "defeated": true}
  }},
  "6": {"armies": {
    "17000885": {"id": 17000885, "o": 12, "s": 25, "l": 500, "u": [
      {"@c": "u", "t": 50617, "s": 1, "n": []},
      {"@c": "u", "t": 2, "s": 24}
    ]},
    "17000886": {"id": 17000886, "o": 29, "s": 3, "l": 501, "u": [{"@c": "u", "t": 2, "s": 3}],
      "c": [{"@c": "dwc", "waitSeconds": 10800, "execTime": 1788993987980}]}
  }},
  "11": {"allUnitTypes": {
    "50617": {"unitTypeId": 50617, "unitName": "Фиона «Мейв» Портер",
              "ratingConfig": {"unitRoles": ["DEPLOY_INFANTRY"]}},
    "2": {"unitTypeId": 2, "unitName": "Пехота", "ratingConfig": {"unitRoles": []}}
  }},
  "3": {"map": {"locations": [
    {"@c": "p", "id": 430, "n": "Афины", "o": 12, "m": 100},
    {"@c": "p", "id": 431, "n": "Лариса", "o": 12, "m": 87.5},
    {"@c": "p", "id": 500, "n": "Париж", "o": 29, "m": 60},
    {"@c": "p", "id": 501, "n": "Ничьё", "m": 50},
    {"@c": "sp", "id": 900, "n": "Эгейское море"}
  ]}},
  "12": {"dayOfGame": 13}
 }
}`

func TestGameStateBuild(t *testing.T) {
	var resp gameStateResponse
	if err := json.Unmarshal([]byte(gameStateFixture), &resp); err != nil {
		t.Fatalf("разбор: %v", err)
	}

	g, err := resp.build("10886819", 12)
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}

	if g.Day != 13 {
		t.Errorf("день = %d, ожидался 13", g.Day)
	}
	// Суша попадает в список целиком, включая ничейную (Owner = 0) — без
	// неё не нарисовать карту. Море не попадает, служебный игрок -1 тоже.
	if len(g.Provinces) != 4 {
		t.Errorf("провинций = %d, ожидалось 4 (вся суша)", len(g.Provinces))
	}
	for _, p := range g.Provinces {
		if p.Name == "Ничьё" && p.Owner != 0 {
			t.Errorf("у ничейной провинции владелец %d", p.Owner)
		}
	}
	if _, ok := g.Players[-1]; ok {
		t.Error("служебный игрок -1 не должен попадать в список")
	}
	if got := g.Players[12].Nation; got != "Греция" {
		t.Errorf("страна = %q", got)
	}
	if !g.Players[29].IsAI || !g.Players[29].Defeated {
		t.Error("ИИ и поражение должны читаться из профиля")
	}

	mine := g.Owned(12)
	if len(mine) != 2 {
		t.Fatalf("своих провинций = %d, ожидалось 2", len(mine))
	}
	if !mine[0].Capital {
		t.Error("столица должна отмечаться по capitalID игрока")
	}
	if mine[1].Capital {
		t.Error("обычная провинция отмечена столицей")
	}
	if mine[1].Morale != 87.5 {
		t.Errorf("мораль = %v, ожидалось 87.5 (игра шлёт и дробные)", mine[1].Morale)
	}
}

// Без карты отчёт был бы пустым и выглядел бы как «мы ничем не владеем» —
// это худший исход, поэтому такой ответ считается ошибкой.
func TestGameStateBuildWithoutMap(t *testing.T) {
	var resp gameStateResponse
	if err := json.Unmarshal([]byte(`{"states":{"12":{"dayOfGame":1}}}`), &resp); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if _, err := resp.build("1", 12); err == nil {
		t.Error("ожидалась ошибка на ответе без карты")
	}
}

// Игру обновляют без нас: на устаревшей версии клиента сервер отвечает
// исключением, в котором и назван нужный номер.
func TestGSExceptionVersion(t *testing.T) {
	e := &gsException{
		class: "ultshared.UltClientVersionMismatchException",
		detail: "Client version mismatch. User client/version for s1914-client-mobile: #316, " +
			"Deployed client version s1914-client-mobile_live: #317",
	}
	if got := e.version(); got != "317" {
		t.Errorf("версия = %q, ожидалось 317", got)
	}

	other := &gsException{class: "ultshared.UltNoSuchGameException", detail: "no game"}
	if got := other.version(); got != "" {
		t.Errorf("на чужом отказе версия не выводится, получили %q", got)
	}
}

// Армию с героиней ищем по роли DEPLOY_INFANTRY, а не по конкретному номеру
// типа: в другой партии он будет другим.
func TestDeployArmyFindsHero(t *testing.T) {
	var resp gameStateResponse
	if err := json.Unmarshal([]byte(gameStateFixture), &resp); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	g, err := resp.build("10886819", 12)
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}

	army, hero, ok := g.DeployArmy()
	if !ok {
		t.Fatal("армия с героиней не нашлась")
	}
	if army.ID != 17000885 {
		t.Errorf("армия = %d, ожидалась 17000885", army.ID)
	}
	if hero != "Фиона «Мейв» Портер" {
		t.Errorf("герой = %q", hero)
	}

	// Чужая армия с такой же пехотой не должна приниматься за нашу.
	g.Me = 29
	if _, _, ok := g.DeployArmy(); ok {
		t.Error("у игрока 29 героини нет, а армия нашлась")
	}
}

// Тело команды сверено с запросом живого клиента: игра ждёт армию
// в сокращённом виде, пустую команду duc и поле n только у героини.
func TestArmyWireMatchesClient(t *testing.T) {
	var resp gameStateResponse
	if err := json.Unmarshal([]byte(gameStateFixture), &resp); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	g, _ := resp.build("10886819", 12)
	army, _, _ := g.DeployArmy()

	got, err := json.Marshal(armyWire(army))
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	const want = `{"@c":"a","ag":0,"au":0,"c":[{"@c":"duc"}],"fm":0,"id":17000885,"o":12,"s":25,` +
		`"u":[{"@c":"u","n":[],"s":1,"t":50617},{"@c":"u","s":24,"t":2}]}`
	if string(got) != want {
		t.Errorf("тело команды:\n получили %s\n ожидалось %s", got, want)
	}
}

// Идущее размещение видно по команде dwc: пока она стоит, героиня занята,
// и повторное нажатие только соврало бы об успехе.
func TestArmyDeployingIsSeen(t *testing.T) {
	var resp gameStateResponse
	if err := json.Unmarshal([]byte(gameStateFixture), &resp); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	g, err := resp.build("10886819", 12)
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}

	var busy, idle *Army
	for i := range g.Armies {
		if g.Armies[i].ID == 17000886 {
			busy = &g.Armies[i]
		}
		if g.Armies[i].ID == 17000885 {
			idle = &g.Armies[i]
		}
	}
	if busy == nil || !busy.Deploying {
		t.Fatal("армия с командой dwc должна считаться размещающейся")
	}
	// 1788993987980 - 1788984426329 ≈ 159 минут по часам сервера.
	if mins := int(busy.DeployLeft.Minutes()); mins != 159 {
		t.Errorf("остаток = %d мин, ожидалось 159", mins)
	}
	if idle == nil || idle.Deploying {
		t.Error("армия без dwc размещающейся не считается")
	}
}
