package supremacy

import "testing"

// Выключатель считает только свои провинции и узнаёт здание по картинке:
// у уровней пункта призыва номера разные, а картинка общая.
func TestSwitches(t *testing.T) {
	g := &GameState{
		Me: 7,
		Upgrades: map[int]Upgrade{
			18: {ID: 18, Image: "railway"},
			21: {ID: 21, Image: "recruiting_office"},
			22: {ID: 22, Image: "recruiting_office"},
			5:  {ID: 5, Image: "fortress"},
		},
		Provinces: []Province{
			{ID: 1, Owner: 7, Built: []int{18, 21, 5}, Condition: map[int]int{18: 60, 21: 20}},
			{ID: 2, Owner: 7, Built: []int{18, 22}, Off: map[int]bool{18: true}},
			{ID: 3, Owner: 9, Built: []int{18, 21}},
		},
	}

	got := g.Switches()
	if got[SwitchRailway] != (SwitchCount{On: 1, Off: 1}) {
		t.Errorf("ЖД: %+v", got[SwitchRailway])
	}
	if got[SwitchRecruiting] != (SwitchCount{On: 2}) {
		t.Errorf("пункты призыва: %+v", got[SwitchRecruiting])
	}

	off := g.switchTargets(SwitchRailway, false)
	if len(off) != 1 || off[0] != (switchTarget{Province: 1, UpgradeID: 18, Condition: 60}) {
		t.Fatalf("выключать: %+v", off)
	}
	on := g.switchTargets(SwitchRailway, true)
	if len(on) != 1 || on[0].Province != 2 {
		t.Fatalf("включать: %+v", on)
	}

	// Копия меняется, общее состояние — нет.
	after := g.withSwitched(off, false)
	if after.Switches()[SwitchRailway] != (SwitchCount{Off: 2}) {
		t.Errorf("после выключения: %+v", after.Switches()[SwitchRailway])
	}
	if g.Switches()[SwitchRailway] != (SwitchCount{On: 1, Off: 1}) {
		t.Error("исходное состояние изменилось")
	}
	if back := after.withSwitched(after.switchTargets(SwitchRailway, true), true); back.Switches()[SwitchRailway] != (SwitchCount{On: 2}) {
		t.Errorf("после включения: %+v", back.Switches()[SwitchRailway])
	}
}

// Пункт призыва ставится первым уровнем и только туда, где нет ни одного
// его уровня и он не строится.
func TestMissing(t *testing.T) {
	g := &GameState{
		Me: 7,
		Upgrades: map[int]Upgrade{
			21: {ID: 21, Image: "recruiting_office", Tier: 1},
			22: {ID: 22, Image: "recruiting_office", Tier: 2, Replaces: 21},
			18: {ID: 18, Image: "railway", Tier: 1},
		},
		Provinces: []Province{
			{ID: 4, Owner: 7, Built: []int{18}},
			{ID: 1, Owner: 7, Built: []int{22}},
			{ID: 2, Owner: 7, Building: []Construction{{UpgradeID: 21}}},
			{ID: 3, Owner: 7},
			{ID: 5, Owner: 9},
		},
	}
	first, where, ok := g.Missing(SwitchRecruiting)
	if !ok || first.ID != 21 {
		t.Fatalf("первый уровень: %+v %v", first, ok)
	}
	if len(where) != 2 || where[0].ID != 3 || where[1].ID != 4 {
		t.Errorf("провинции: %+v", where)
	}
}
