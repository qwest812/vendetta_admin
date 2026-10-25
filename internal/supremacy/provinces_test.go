package supremacy

import (
	"context"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

// Владельцы — только живые игроки с номером на сайте; бот и ничья земля
// в счёт не идут.
func TestOwnersOf(t *testing.T) {
	st := &GameState{
		Players: map[int]Player{
			1: {ID: 1, SiteUserID: "a", TeamID: 3},
			2: {ID: 2, SiteUserID: "b", TeamID: 3},
			3: {ID: 3, IsAI: true},
		},
		Provinces: []Province{{ID: 10, Owner: 1}, {ID: 11, Owner: 1}, {ID: 12, Owner: 2}, {ID: 13, Owner: 3}, {ID: 14}},
	}
	owners, counts, teams := ownersOf(st)
	if len(owners) != 3 || owners[12] != "b" || counts["a"] != 2 || counts["b"] != 1 || teams["a"] != 3 {
		t.Errorf("owners %v counts %v teams %v", owners, counts, teams)
	}
}

// Переход внутри коалиции и между врагами считаются отдельно; у бота
// и ничьей земли переходов нет — их нет и в owners.
func TestTransfersBetween(t *testing.T) {
	prev := map[int]string{1: "b", 2: "b", 3: "b", 4: "c", 5: "a"}
	cur := map[int]string{1: "a", 2: "a", 3: "b", 4: "a", 5: "a"}
	teams := map[string]int{"a": 1, "b": 1, "c": 2}
	got := transfersBetween(prev, cur, teams)
	if len(got) != 2 {
		t.Fatalf("переходы: %+v", got)
	}
	if got[0] != (domain.ProvinceTransfer{From: "b", To: "a", SameTeam: true, Provinces: 2}) {
		t.Errorf("внутри коалиции: %+v", got[0])
	}
	if got[1] != (domain.ProvinceTransfer{From: "c", To: "a", SameTeam: false, Provinces: 1}) {
		t.Errorf("завоевание: %+v", got[1])
	}
	// Провинция пропала из нынешних (ушла боту) — не переход между людьми.
	if n := len(transfersBetween(map[int]string{9: "a"}, map[int]string{}, teams)); n != 0 {
		t.Errorf("уход боту засчитан: %d", n)
	}
}

type fakeProvinces struct {
	prev  map[int]string
	saved []domain.ProvinceSnapshot
}

func (f *fakeProvinces) PrevOwners(context.Context, string) (map[int]string, error) {
	return f.prev, nil
}

func (f *fakeProvinces) SaveProvinces(_ context.Context, s domain.ProvinceSnapshot, _ time.Time) error {
	f.saved = append(f.saved, s)
	f.prev = s.Owners
	return nil
}

// Два захода подряд: второй видит, что провинция перешла от b к a внутри
// коалиции. Заходов в игру — ровно столько же, сколько за коалициями.
func TestScannerKeepsProvinces(t *testing.T) {
	state := &GameState{
		GameID: "7", Day: 4,
		Players: map[int]Player{
			1: {ID: 1, SiteUserID: "a", TeamID: 1},
			2: {ID: 2, SiteUserID: "b", TeamID: 1},
		},
		Teams:     map[int]Team{1: {ID: 1, Name: "Союз"}},
		Provinces: []Province{{ID: 10, Owner: 1}, {ID: 11, Owner: 2}},
	}
	obs := &fakeObserver{state: state}
	store := newFakeStore(domain.WatchedGame{GameID: "7", Speed: 4})
	prov := &fakeProvinces{}
	s := NewCoalitionScanner(obs, store, fakeSwitch(true), 0, quietLog())
	s.pause = 0
	s.KeepProvinces(prov)

	s.tick(context.Background())
	state.Day = 6
	state.Provinces = []Province{{ID: 10, Owner: 1}, {ID: 11, Owner: 1}}
	store.next["7"] = time.Time{}
	s.tick(context.Background())

	if obs.calls != 2 || len(prov.saved) != 2 {
		t.Fatalf("заходов %d, записей %d", obs.calls, len(prov.saved))
	}
	if len(prov.saved[0].Transfers) != 0 {
		t.Errorf("первый заход нашёл переходы: %+v", prov.saved[0].Transfers)
	}
	tr := prov.saved[1].Transfers
	if len(tr) != 1 || tr[0].From != "b" || tr[0].To != "a" || !tr[0].SameTeam || prov.saved[1].Counts["a"] != 2 {
		t.Errorf("второй заход: %+v", prov.saved[1])
	}
}

// Цена призыва — по уровню: открытый предметом самый дорогой уровень.
// Ничего не открыто — первый, самый дешёвый.
func TestDeployCost(t *testing.T) {
	st := &GameState{
		DeployLevels: map[int][]DeployLevel{50617: {
			{Item: 1, Cost: map[int]float64{resFish: 300, resMoney: 8000}, Amount: 2},
			{Item: 2, Cost: map[int]float64{resFish: 700, resMoney: 18000}, Amount: 6},
			{Item: 3, Cost: map[int]float64{resFish: 1000, resMoney: 25000}, Amount: 9},
		}},
		Inventory: map[int]int{1: 1, 2: 1},
	}
	if c, ok := st.DeployCost(50617); !ok || c.Cost[resMoney] != 18000 || c.Amount != 6 {
		t.Errorf("десятый уровень: %+v ok=%v", c, ok)
	}
	st.Inventory = nil
	if c, _ := st.DeployCost(50617); c.Cost[resMoney] != 8000 {
		t.Errorf("ничего не открыто: %+v", c)
	}
	if _, ok := st.DeployCost(64); ok {
		t.Error("цена у героя без уровней")
	}
}

// Призыв пропускается, если после него денег останется меньше порога
// или рыбы не хватит. Цена неизвестна — порог против того, что есть.
func TestDeploySkip(t *testing.T) {
	now := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	res := func(money, fish float64) map[int]Resource {
		return map[int]Resource{resMoney: {Amount: money, Measured: now}, resFish: {Amount: fish, Measured: now}}
	}
	priced := map[int][]DeployLevel{7: {{Item: 1, Cost: map[int]float64{resFish: 700, resMoney: 18000}}}}
	inv := map[int]int{1: 1}

	cases := []struct {
		name string
		st   *GameState
		min  int
		skip bool
	}{
		{"хватает с запасом", &GameState{Resources: res(40000, 1000), DeployLevels: priced, Inventory: inv}, 20000, false},
		{"после призыва меньше порога", &GameState{Resources: res(35000, 1000), DeployLevels: priced, Inventory: inv}, 20000, true},
		{"рыбы не хватает", &GameState{Resources: res(90000, 500), DeployLevels: priced, Inventory: inv}, 20000, true},
		{"цена неизвестна, денег больше порога", &GameState{Resources: res(25000, 0)}, 20000, false},
		{"цена неизвестна, денег меньше порога", &GameState{Resources: res(15000, 0)}, 20000, true},
		{"порог ноль, цена неизвестна", &GameState{Resources: res(100, 0)}, 0, false},
		{"денег игра не прислала", &GameState{DeployLevels: priced, Inventory: inv}, 20000, false},
	}
	for _, c := range cases {
		if why := deploySkip(c.st, 7, c.min, now); (why != "") != c.skip {
			t.Errorf("%s: %q", c.name, why)
		}
	}
}
