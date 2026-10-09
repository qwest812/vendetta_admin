package supremacy

import (
	"encoding/json"
	"testing"
)

// Таблицы игры бывают объектом со служебным «@c» и пустым массивом:
// ни то ни другое не должно ни ронять разбор, ни давать мусор.
func TestNumMap(t *testing.T) {
	got := numMap(json.RawMessage(`{"@c":"java.util.HashMap","1":2.5,"20":"x","3":4}`))
	if len(got) != 2 || got[1] != 2.5 || got[3] != 4 {
		t.Fatalf("numMap = %v", got)
	}
	if got := numMap(json.RawMessage(`[]`)); len(got) != 0 {
		t.Fatalf("numMap([]) = %v", got)
	}
}

func TestStateFactorsAndNames(t *testing.T) {
	f := stateFactors(json.RawMessage(`[{"id":0,"consumptionFactor":1},{"id":1,"consumptionFactor":0.5},{"id":2}]`))
	if len(f) != 2 || f[1] != 0.5 {
		t.Fatalf("stateFactors = %v", f)
	}
	n := resourceNames(json.RawMessage(`{"@c":"java.util.HashMap","1":{"resourceID":1,"name":"Зерно"},"x":{"resourceID":5,"name":"Нефть"}}`))
	if n[1] != "Зерно" || n[5] != "Нефть" {
		t.Fatalf("resourceNames = %v", n)
	}
}

// Баланс складывается так же, как у клиента игры: добыча провинций минус
// население, здания (своя доля по состоянию плюс заменённые уровни целиком)
// и войска по численности.
func TestEconomies(t *testing.T) {
	g := &GameState{
		Me:      1,
		Players: map[int]Player{1: {ID: 1, Nation: "Мы"}, 2: {ID: 2, Nation: "Они"}},
		Provinces: []Province{
			{ID: 10, Owner: 1, Population: 1000, StateID: 1, Output: map[int]float64{1: 500},
				Built: []int{7}, Condition: map[int]int{7: 2}},
			{ID: 11, Owner: 1, Population: 1000, Output: map[int]float64{1: 300}},
			{ID: 12, Owner: 2, Population: 999, Output: map[int]float64{1: 100}},
			{ID: 13, Owner: 0, Output: map[int]float64{1: 1000}},
		},
		Upgrades: map[int]Upgrade{
			6: {ID: 6, DailyCosts: map[int]float64{1: 10}},
			7: {ID: 7, Replaces: 6, MaxCondition: 4, DailyCosts: map[int]float64{1: 40}},
		},
		Consumption: map[int]float64{1: 0.1},
		StateFactor: map[int]float64{1: 0.5},
		UnitUpkeep:  map[int]map[int]float64{3: {1: 2}},
		Armies: []Army{
			{Owner: 2, Units: []ArmyUnit{{Type: 3, Size: 5}}},
			{Owner: 9, Units: []ArmyUnit{{Type: 3, Size: 100}}}, // без провинций
		},
		Resources: map[int]Resource{1: {ID: 1, Daily: 555}},
	}
	list := g.Economies()
	if len(list) != 2 {
		t.Fatalf("игроков %d, want 2", len(list))
	}
	us, them := list[0], list[1]
	if us.Player.ID != 1 || us.Provinces != 2 {
		t.Fatalf("первым должны идти мы с двумя провинциями: %+v", us)
	}
	// население: floor(1000×0.1)×0.5 + floor(1000×0.1) = 50 + 100
	// здания: 40×2/4 + 10 = 30
	if us.Output[1] != 800 || us.Populace[1] != 150 || us.Buildings[1] != 30 || us.Net(1) != 620 {
		t.Fatalf("мы: %+v, net %v", us, us.Net(1))
	}
	if us.Actual[1] != 555 {
		t.Fatalf("сверка: %v", us.Actual)
	}
	// floor(999×0.1) = 99, войска 5×2 = 10
	if them.Populace[1] != 99 || them.Army[1] != 10 || them.Net(1) != -9 || them.Actual != nil {
		t.Fatalf("они: %+v", them)
	}
}
