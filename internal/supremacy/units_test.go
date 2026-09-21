package supremacy

import (
	"encoding/json"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

// unitState — партия, где мы играем за первого, со справочником из одного
// заказываемого войска — бронеавтомобиля.
func unitState(now time.Time, provinces ...Province) *GameState {
	g := buildState(provinces, plenty(now))
	g.Units = map[int]UnitType{
		198: {ID: 198, Name: "Бронеавтомобиль", Image: "car", Cost: map[int]float64{20: 8500}},
	}
	return g
}

var carWire = json.RawMessage(`{"@c":"su","c":0,"e":true,"unit":{"@c":"u","t":198,"m":1.0,"s":1,"k":0}}`)

// Производство свободно, провинция умеет — войско заказывается.
func TestPlanQueuesOrdersUnit(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := unitState(now, Province{ID: 7, Name: "Берлин", Owner: 1,
		CanProduce: map[int]json.RawMessage{198: carWire}})

	plan := PlanQueues(g, domain.BuildQueues{Units: map[int][]int{7: {198}}}, now)
	if len(plan.Start) != 1 || plan.Start[0].Kind != domain.QueueUnit || plan.Start[0].UpgradeID != 198 {
		t.Fatalf("войско не заказано: %+v", plan)
	}
}

// Нужного здания нет — заказ ждёт и говорит почему, но заход ради него
// не назначается: здание разбудит воркер концом своей стройки.
func TestPlanQueuesUnitNeedsBuilding(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := unitState(now, Province{ID: 7, Name: "Берлин", Owner: 1})

	plan := PlanQueues(g, domain.BuildQueues{Units: map[int][]int{7: {198}}}, now)
	if len(plan.Start) != 0 || !plan.Next.IsZero() || len(plan.Notes) != 1 {
		t.Fatalf("ждали заметку без заказа: %+v", plan)
	}
}

// Производство занято — приходим к его концу, а стройку это не держит:
// у них разные слоты.
func TestPlanQueuesUnitWaitsForProduction(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	ends := now.Add(3 * time.Hour)
	g := unitState(now, Province{ID: 7, Name: "Берлин", Owner: 1, Slots: 1, ProdSlots: 1,
		Producing:  []Production{{UnitTypeID: 198, Ends: ends}},
		CanProduce: map[int]json.RawMessage{198: carWire}})

	plan := PlanQueues(g, domain.BuildQueues{
		Buildings: map[int][]int{7: {16}},
		Units:     map[int][]int{7: {198}},
	}, now)
	if len(plan.Start) != 1 || plan.Start[0].Kind != domain.QueueBuilding {
		t.Fatalf("стройка должна идти, пока занято производство: %+v", plan)
	}
	if !plan.Next.Equal(ends.Add(buildAfter)) {
		t.Fatalf("следующий заход %s, ждали %s", plan.Next, ends.Add(buildAfter))
	}
}

// Сырьё общее: денег хватает на одно войско — вторая провинция ждёт,
// а не лезет в те же деньги.
func TestPlanQueuesSharesResources(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	can := map[int]json.RawMessage{198: carWire}
	g := unitState(now,
		Province{ID: 7, Name: "Берлин", Owner: 1, CanProduce: can},
		Province{ID: 8, Name: "Гамбург", Owner: 1, CanProduce: can})
	g.Resources[20] = Resource{ID: 20, Name: "Деньги", Amount: 10000, Measured: now, Rate: 1}

	plan := PlanQueues(g, domain.BuildQueues{Units: map[int][]int{7: {198}, 8: {198}}}, now)
	if len(plan.Start) != 1 || plan.Start[0].ProvinceID != 7 {
		t.Fatalf("ждали один заказ в первой провинции: %+v", plan)
	}
	// Второму нужно 17000 всего при 10000 и приросте 1 в секунду.
	want := clampVisit(now.Add(7000*time.Second).Add(buildAfter), now)
	if !plan.Next.Equal(want) {
		t.Fatalf("следующий заход %s, ждали %s", plan.Next, want)
	}
}

// Производство и то, что провинция может производить, — в том виде,
// в каком их шлёт игра (снято с 10909474).
func TestProductionParsing(t *testing.T) {
	raw := json.RawMessage(`["ultshared.UltProductionList",[{"@c":"ultshared.UltProvinceProduction","u":{"@c":"su","c":0,"e":true,"unit":{"@c":"u","t":198,"m":1.0,"s":1}},"t":1790255965932,"s":1790174456932,"b":-1}]]`)
	got := productionList(raw, gameClock{scale: 1})
	if len(got) != 1 || got[0].UnitTypeID != 198 || !got[0].Ends.Equal(time.UnixMilli(1790255965932)) {
		t.Fatalf("производство: %+v", got)
	}
	if got := productionList(json.RawMessage(`["ultshared.UltProductionList",[null]]`), gameClock{scale: 1}); len(got) != 0 {
		t.Fatalf("пустой слот дал производство: %+v", got)
	}

	can := possibleUnits([]json.RawMessage{carWire, json.RawMessage(`{"@c":"su","unit":{"t":28}}`)})
	if len(can) != 2 || string(can[198]) != string(carWire) {
		t.Fatalf("что можно производить: %v", can)
	}
}

// Имя картинки попадает в адрес, только если похоже на имя файла игры.
func TestImageURLs(t *testing.T) {
	if got := UnitImageURL("car"); got != imageBase+"units/car_s3.png" {
		t.Errorf("войско: %s", got)
	}
	if got := UpgradeImageURL("railway"); got != imageBase+"upgrades/railway_s5.png" {
		t.Errorf("здание: %s", got)
	}
	for _, bad := range []string{"", "../x", "Car", "a b", "x?y"} {
		if UnitImageURL(bad) != "" || UpgradeImageURL(bad) != "" {
			t.Errorf("негодное имя %q попало в адрес", bad)
		}
	}
}
