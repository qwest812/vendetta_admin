package supremacy

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

// roadsField собирает поле connections_v2 из записей: провинция, тип
// и концы отрезка в сотых долях — так его хранит файл карты.
func roadsField(records ...[6]int32) string {
	raw := make([]byte, 0, 24*len(records))
	for _, r := range records {
		for _, v := range r {
			raw = binary.BigEndian.AppendUint32(raw, uint32(v))
		}
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// Дороги суши разбираются, а морские — с провинцией −1 или морской —
// отбрасываются: сухопутную армию по ним без кораблей не отправить.
func TestDecodeRoads(t *testing.T) {
	field := roadsField(
		[6]int32{489, 0, 214600, 12000, 211350, 14050},
		[6]int32{-1, 0, 100, 100, 200, 200},
		[6]int32{7, 0, 100, 100, 200, 200},
	)
	land := map[int]MapPoint{489: {X: 2146, Y: 120}}
	roads, err := decodeRoads(field, land)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	want := Road{Province: 489, A: MapPoint{X: 2146, Y: 120}, B: MapPoint{X: 2113.5, Y: 140.5}}
	if len(roads) != 1 || roads[0] != want {
		t.Errorf("дороги = %+v, ожидалась одна %+v", roads, want)
	}

	if _, err := decodeRoads(base64.StdEncoding.EncodeToString(make([]byte, 25)), land); err == nil {
		t.Error("обрезанная запись разобралась без ошибки")
	}
}

// Кусок карты 51_5 вокруг живого приказа 14.10.2026: армия стояла
// на дороге в провинции 489 и шла в центр соседней 135.
func liveRoads() *MapGeometry {
	return &MapGeometry{
		Centers: map[int]MapPoint{489: {X: 2146, Y: 120}, 135: {X: 2089, Y: 130}, 478: {X: 2130, Y: 200}},
		Roads: []Road{
			{Province: 489, A: MapPoint{X: 2146, Y: 120}, B: MapPoint{X: 2197.68, Y: 96.31}},
			{Province: 489, A: MapPoint{X: 2113.5, Y: 140.5}, B: MapPoint{X: 2146, Y: 120}},
			{Province: 135, A: MapPoint{X: 2095.33, Y: 147.67}, B: MapPoint{X: 2113.5, Y: 140.5}},
			{Province: 135, A: MapPoint{X: 2089, Y: 130}, B: MapPoint{X: 2095.33, Y: 147.67}},
			// Обход через 478 — длиннее: им путь идти не должен.
			{Province: 478, A: MapPoint{X: 2146, Y: 120}, B: MapPoint{X: 2130, Y: 200}},
			{Province: 478, A: MapPoint{X: 2130, Y: 200}, B: MapPoint{X: 2089, Y: 130}},
		},
	}
}

// Путь совпадает с тем, что проложил клиент игры: от точки армии
// посреди дороги через центр 489 и две точки на границе — в центр 135.
func TestFindRouteMatchesClient(t *testing.T) {
	geo := liveRoads()
	from := MapPoint{X: 2152.3150740722026, Y: 117.10517960378765}
	path, ok := FindRoute(geo, from, geo.Centers[135], func(int) float64 { return 1 })
	if !ok {
		t.Fatal("путь не нашёлся")
	}
	want := []MapPoint{from, {X: 2146, Y: 120}, {X: 2113.5, Y: 140.5}, {X: 2095.33, Y: 147.67}, {X: 2089, Y: 130}}
	if len(path) != len(want) {
		t.Fatalf("путь = %v, ожидался %v", path, want)
	}
	for i := range want {
		if path[i] != want[i] {
			t.Errorf("точка %d = %v, ожидалась %v", i, path[i], want[i])
		}
	}

	// Тело приказа сверено с запросом живого клиента.
	got, err := json.Marshal(gotoCommands(path[:3]))
	if err != nil {
		t.Fatalf("сборка: %v", err)
	}
	const wantJSON = `[{"@c":"gc","sp":{"x":2152.3150740722026,"y":117.10517960378765},"tp":{"x":2146,"y":120}},` +
		`{"@c":"gc","sp":{"x":2146,"y":120},"tp":{"x":2113.5,"y":140.5}}]`
	if string(got) != wantJSON {
		t.Errorf("команды:\n получили %s\n ожидалось %s", got, wantJSON)
	}
}

// Закрытая провинция не проходится: путь идёт в обход, а без обхода
// его нет. Армию вдали от любой дороги не ведём вовсе — не гадаем.
func TestFindRouteRespectsCost(t *testing.T) {
	geo := liveRoads()
	no135 := func(p int) float64 {
		if p == 489 {
			return 0
		}
		return 1
	}
	path, ok := FindRoute(geo, geo.Centers[489], geo.Centers[135], no135)
	if !ok || len(path) != 3 || path[1] != (MapPoint{X: 2130, Y: 200}) {
		t.Errorf("путь в обход 489 = %v, %v", path, ok)
	}

	if _, ok := FindRoute(geo, MapPoint{X: 10, Y: 10}, geo.Centers[135], func(int) float64 { return 1 }); ok {
		t.Error("армию вдали от дорог повели")
	}
}

// rallyState — партия, где мы игрок 6, а провинции 489 и 135 наши.
func rallyState(armies ...Army) *GameState {
	return &GameState{
		Me:        6,
		Provinces: []Province{{ID: 489, Owner: 6}, {ID: 135, Owner: 6}, {ID: 478, Owner: 9}},
		UnitSets:  map[int]int{2: unitSetLand, 50617: unitSetHero, 3: 2, 4: 3},
		Armies:    armies,
	}
}

func infantry(id int64, location int, at MapPoint) Army {
	return Army{ID: id, Owner: 6, Size: 1, Location: location, Pos: at, Units: []ArmyUnit{{Type: 2, Size: 1}}}
}

// Отбор армий: только наши сухопутные, не в бою, не Мейв в размещении
// и не те, что уже в городе или идут туда. Остальных считаем по причинам.
func TestPlanRally(t *testing.T) {
	geo := liveRoads()
	center489 := geo.Centers[489]
	city := geo.Centers[135]

	plane := infantry(3, 489, center489)
	plane.Units = []ArmyUnit{{Type: 3, Size: 1}}
	fighting := infantry(4, 489, center489)
	fighting.Fighting = true
	maeve := infantry(5, 489, center489)
	maeve.Units = append(maeve.Units, ArmyUnit{Type: 50617, Size: 1})
	marching := infantry(7, 489, center489)
	marching.MarchTo = &city
	foreign := infantry(8, 489, center489)
	foreign.Owner = 9
	atSea := infantry(9, 700, MapPoint{X: 1, Y: 1})

	state := rallyState(infantry(1, 489, center489), infantry(2, 135, city), plane, fighting, maeve,
		marching, foreign, atSea)
	plan, err := PlanRally(state, geo, 135, func(int64) bool { return true }, 5)
	if err != nil {
		t.Fatalf("план: %v", err)
	}
	if len(plan.Orders) != 1 || plan.Orders[0].Army.ID != 1 {
		t.Fatalf("приказы = %+v, ожидался один, армии 1", plan.Orders)
	}
	if plan.There != 2 || plan.Fighting != 1 || plan.Deploying != 1 || plan.NotLand != 2 || plan.NoRoute != 0 {
		t.Errorf("пропуски: %+v", plan)
	}
	// Список армий — все наши, и пропущенные тоже: по нему автопилот
	// узнает новые. Чужая армия в нём не нужна.
	if len(plan.Armies) != 7 {
		t.Errorf("наших армий = %v, ожидалось семь", plan.Armies)
	}

	summary := plan.Summary("Город")
	for _, part := range []string{"в Город отправлено армий: 1", "уже там или идут туда: 2", "в бою: 1"} {
		if !strings.Contains(summary, part) {
			t.Errorf("в итоге %q нет %q", summary, part)
		}
	}
}

// Автопилот трогает только новые армии; пока он не видел ни одной,
// новых нет — первый заход лишь запоминает, что есть.
func TestNewArmies(t *testing.T) {
	if newArmies(nil)(1) {
		t.Error("при пустой памяти армия сочтена новой")
	}
	pick := newArmies([]int64{1, 2})
	if pick(1) || !pick(3) {
		t.Error("новыми должны считаться только незнакомые армии")
	}

	geo := liveRoads()
	state := rallyState(infantry(1, 489, geo.Centers[489]), infantry(3, 489, geo.Centers[489]))
	plan, err := PlanRally(state, geo, 135, pick, 0)
	if err != nil {
		t.Fatalf("план: %v", err)
	}
	if len(plan.Orders) != 1 || plan.Orders[0].Army.ID != 3 {
		t.Errorf("приказы = %+v, ожидалась только новая армия 3", plan.Orders)
	}
}

// Город, которого нет на карте суши, — ошибка, а не пустой план.
func TestPlanRallyUnknownCity(t *testing.T) {
	if _, err := PlanRally(rallyState(), liveRoads(), 999, func(int64) bool { return true }, 0); err == nil {
		t.Error("ожидалась ошибка на неизвестный город")
	}
}
