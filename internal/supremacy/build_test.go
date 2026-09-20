package supremacy

import (
	"encoding/json"
	"testing"
	"time"
)

// state собирает партию, в которой мы играем за первого, с одной
// провинцией и справочником из двух зданий.
func buildState(provinces []Province, resources map[int]Resource) *GameState {
	return &GameState{
		Me:        1,
		Provinces: provinces,
		Upgrades: map[int]Upgrade{
			16: {ID: 16, Name: "Крепость", Build: 8 * time.Hour,
				Cost: map[int]float64{2: 2000, 20: 4000}},
			19: {ID: 19, Name: "Завод", Build: 48 * time.Hour,
				Cost: map[int]float64{2: 2500}},
		},
		Resources: resources,
	}
}

func plenty(now time.Time) map[int]Resource {
	return map[int]Resource{
		2:  {ID: 2, Name: "Железо", Amount: 100000, Measured: now, Rate: 1},
		20: {ID: 20, Name: "Деньги", Amount: 100000, Measured: now, Rate: 1},
	}
}

// Свободный слот и полные склады — здание ставится сразу, а прийти снова
// надо скоро: настоящее время окончания стройки скажет только сама игра.
func TestPlanBuildsStarts(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := buildState([]Province{{ID: 7, Name: "Берлин", Owner: 1, Slots: 1}}, plenty(now))

	plan := PlanBuilds(g, map[int][]int{7: {16, 19}}, now)
	if len(plan.Start) != 1 {
		t.Fatalf("ожидалась одна постройка, вышло %d: %+v", len(plan.Start), plan)
	}
	if plan.Start[0].UpgradeID != 16 || plan.Start[0].Province != "Берлин" {
		t.Errorf("поставили не то: %+v", plan.Start[0])
	}
	// План считает только события самой партии; ждать ли перепроверки,
	// решает заход — он один знает, встало здание или игра отказала.
	if !plan.Next.IsZero() {
		t.Errorf("плану ждать нечего, а он назначил заход на %v", plan.Next)
	}
}

// Срок следующего захода после самого захода. Встало здание — вернёмся
// скоро, за настоящим временем окончания; отказали — не раньше чем через
// час, иначе в партию пришлось бы ходить каждые две минуты; а событие
// самой партии важнее обоих.
func TestNextVisit(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	var never time.Time

	tests := []struct {
		name            string
		events          time.Time
		started, failed int
		want            time.Time
	}{
		{"поставили", never, 1, 0, now.Add(buildRecheck)},
		{"отказали", never, 0, 1, now.Add(buildRefused)},
		{"нечего делать", never, 0, 0, never},
		{"событие раньше перепроверки", now.Add(time.Minute), 1, 0, now.Add(time.Minute)},
		{"событие позже отказа", now.Add(3 * time.Hour), 0, 1, now.Add(buildRefused)},
		{"событие раньше отказа", now.Add(10 * time.Minute), 0, 1, now.Add(10 * time.Minute)},
		{"слишком далёкое событие", now.Add(48 * time.Hour), 0, 0, now.Add(buildLatest)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextVisit(tt.events, tt.started, tt.failed, now)
			if !got.Equal(tt.want) {
				t.Errorf("вышло %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

// Занятый слот — ждём конца стройки, своей или начатой руками, и ничего
// не отменяем.
func TestPlanBuildsWaitsForConstruction(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	ends := now.Add(2 * time.Hour)
	g := buildState([]Province{{
		ID: 7, Name: "Берлин", Owner: 1, Slots: 1,
		Building: []Construction{{UpgradeID: 19, Ends: ends}},
	}}, plenty(now))

	plan := PlanBuilds(g, map[int][]int{7: {16}}, now)
	if len(plan.Start) != 0 {
		t.Errorf("при занятом слоте ставить нечего: %+v", plan.Start)
	}
	if want := ends.Add(buildAfter); !plan.Next.Equal(want) {
		t.Errorf("следующий заход %v, ожидался %v", plan.Next, want)
	}
}

// Премиум даёт второй слот: пока он свободен, стройка идёт параллельно.
func TestPlanBuildsUsesSecondSlot(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := buildState([]Province{{
		ID: 7, Name: "Берлин", Owner: 1, Slots: 2,
		Building: []Construction{{UpgradeID: 19, Ends: now.Add(time.Hour)}},
	}}, plenty(now))

	plan := PlanBuilds(g, map[int][]int{7: {16}}, now)
	if len(plan.Start) != 1 {
		t.Errorf("второй слот свободен, здание должно было встать: %+v", plan)
	}
}

// Не хватает сырья — заход назначается к тому мигу, когда его накопится
// ровно сколько нужно. Отказа игры при этом не ждём: считаем сами.
func TestPlanBuildsWaitsForResources(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	res := plenty(now)
	// Железа не хватает 1000, приходит по одной единице в секунду.
	res[2] = Resource{ID: 2, Name: "Железо", Amount: 1000, Measured: now, Rate: 1}
	g := buildState([]Province{{ID: 7, Name: "Берлин", Owner: 1, Slots: 1}}, res)

	plan := PlanBuilds(g, map[int][]int{7: {16}}, now)
	if len(plan.Start) != 0 {
		t.Fatalf("без сырья ставить нельзя: %+v", plan.Start)
	}
	want := now.Add(1000*time.Second + buildAfter)
	if !plan.Next.Equal(want) {
		t.Errorf("следующий заход %v, ожидался %v", plan.Next, want)
	}
	if len(plan.Notes) == 0 {
		t.Error("про нехватку надо сказать словами")
	}
}

// Сырьё не копится вовсе — ждать нечего, и заход из-за такой провинции
// не назначается. Очередь при этом остаётся на месте.
func TestPlanBuildsNeverAfford(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	res := plenty(now)
	res[2] = Resource{ID: 2, Name: "Железо", Amount: 10, Measured: now, Rate: -1}
	g := buildState([]Province{{ID: 7, Name: "Берлин", Owner: 1, Slots: 1}}, res)

	plan := PlanBuilds(g, map[int][]int{7: {16}}, now)
	if len(plan.Start) != 0 || !plan.Next.IsZero() {
		t.Errorf("ждать нечего: %+v", plan)
	}
	if len(plan.Notes) == 0 {
		t.Error("про безнадёжную запись надо сказать словами")
	}
}

// Провинцию отбили — очередь не трогаем, но и ходить ради неё не будем.
func TestPlanBuildsSkipsLostProvince(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := buildState([]Province{{ID: 7, Name: "Берлин", Owner: 2, Slots: 1}}, plenty(now))

	plan := PlanBuilds(g, map[int][]int{7: {16}}, now)
	if len(plan.Start) != 0 || !plan.Next.IsZero() {
		t.Errorf("чужую провинцию не трогаем: %+v", plan)
	}
	if len(plan.Notes) != 1 {
		t.Errorf("про потерянную провинцию надо сказать: %+v", plan.Notes)
	}
}

// Срок захода зажат с обеих сторон: слишком скоро возвращаться в партию
// нельзя, а заснуть навсегда — тем более.
func TestPlanBuildsClampsNext(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	soon := buildState([]Province{{
		ID: 7, Name: "Берлин", Owner: 1, Slots: 1,
		Building: []Construction{{UpgradeID: 19, Ends: now.Add(-time.Hour)}},
	}}, plenty(now))
	if plan := PlanBuilds(soon, map[int][]int{7: {16}}, now); !plan.Next.Equal(now.Add(buildSoonest)) {
		t.Errorf("слишком скорый заход не подтянулся: %v", plan.Next)
	}

	late := buildState([]Province{{
		ID: 7, Name: "Берлин", Owner: 1, Slots: 1,
		Building: []Construction{{UpgradeID: 19, Ends: now.Add(48 * time.Hour)}},
	}}, plenty(now))
	if plan := PlanBuilds(late, map[int][]int{7: {16}}, now); !plan.Next.Equal(now.Add(buildLatest)) {
		t.Errorf("слишком далёкий заход не подтянулся: %v", plan.Next)
	}
}

// Стройки приходят особым списком, где пустые места означают свободные
// слоты. Разобрать надо и его, и одиночную запись bi.
func TestBuildList(t *testing.T) {
	raw := json.RawMessage(`["ultshared.UltProductionList",[{"u":{"id":20},"t":1786534022728},null]]`)
	got := buildList(raw, nil)
	if len(got) != 1 || got[0].UpgradeID != 20 {
		t.Fatalf("список строек разобрался неверно: %+v", got)
	}
	if want := time.UnixMilli(1786534022728); !got[0].Ends.Equal(want) {
		t.Errorf("время окончания %v, ожидалось %v", got[0].Ends, want)
	}

	single := &buildWire{Ends: 100}
	single.Upgrade.ID = 16
	if got := buildList(nil, single); len(got) != 1 || got[0].UpgradeID != 16 {
		t.Errorf("одиночная стройка разобралась неверно: %+v", got)
	}
	if got := buildList(nil, nil); len(got) != 0 {
		t.Errorf("без строек список должен быть пуст: %+v", got)
	}
}

// Запас растёт линейно, и по нему считается время, когда его хватит.
func TestResourceEnough(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	r := Resource{Amount: 500, Measured: now, Rate: 2}

	if got := r.AmountAt(now.Add(time.Minute)); got != 620 {
		t.Errorf("через минуту ожидалось 620, вышло %v", got)
	}
	if at, ok := r.Enough(400, now); !ok || !at.Equal(now) {
		t.Errorf("хватает уже сейчас: %v %v", at, ok)
	}
	if at, ok := r.Enough(700, now); !ok || !at.Equal(now.Add(100*time.Second)) {
		t.Errorf("ожидалось +100 секунд, вышло %v", at)
	}
	if _, ok := (Resource{Amount: 1, Rate: 0}).Enough(5, now); ok {
		t.Error("без прироста запас не накопится")
	}
}
