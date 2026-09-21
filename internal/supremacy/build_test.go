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
	got := buildList(raw, nil, gameClock{scale: 1})
	if len(got) != 1 || got[0].UpgradeID != 20 {
		t.Fatalf("список строек разобрался неверно: %+v", got)
	}
	if want := time.UnixMilli(1786534022728); !got[0].Ends.Equal(want) {
		t.Errorf("время окончания %v, ожидалось %v", got[0].Ends, want)
	}

	single := &buildWire{Ends: 100}
	single.Upgrade.ID = 16
	if got := buildList(nil, single, gameClock{scale: 1}); len(got) != 1 || got[0].UpgradeID != 16 {
		t.Errorf("одиночная стройка разобралась неверно: %+v", got)
	}
	if got := buildList(nil, nil, gameClock{scale: 1}); len(got) != 0 {
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

// Здание стоит целиком — второй раз его не ставим и заход ради него
// не назначаем, а просим убрать запись.
func TestPlanBuildsSkipsFinished(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := buildState([]Province{{ID: 7, Name: "Сурселе", Owner: 1, Slots: 1,
		Built: []int{16}, Condition: map[int]int{16: 60}}}, plenty(now))
	u := g.Upgrades[16]
	u.BuildCondition = 60
	g.Upgrades[16] = u

	plan := PlanBuilds(g, map[int][]int{7: {16}}, now)
	if len(plan.Start) != 0 || !plan.Next.IsZero() {
		t.Fatalf("построенное снова в плане: %+v", plan)
	}
	if len(plan.Notes) != 1 {
		t.Fatalf("ждали одну заметку, вышло %v", plan.Notes)
	}
}

// Недостроенное (так было с дорогой в Сурселе: 16 из 60) лежит в том же
// списке построенного, но его надо ставить снова — и с нынешним
// состоянием, как шлёт клиент игры.
func TestPlanBuildsRepairsUnfinished(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	g := buildState([]Province{{ID: 484, Name: "Сурселе", Owner: 1, Slots: 1,
		Built: []int{16}, Condition: map[int]int{16: 16}}}, plenty(now))
	u := g.Upgrades[16]
	u.BuildCondition = 60
	g.Upgrades[16] = u

	plan := PlanBuilds(g, map[int][]int{484: {16}}, now)
	if len(plan.Start) != 1 {
		t.Fatalf("недостроенное не поставлено: %+v", plan)
	}
	wire := upgradeWire(g, plan.Start[0])
	if wire["id"] != 16 || wire["c"] != 16 || wire["e"] != true {
		t.Fatalf("здание в действии: %v", wire)
	}

	// Новое здание уходит одним уровнем.
	g.Provinces[0].Built, g.Provinces[0].Condition = nil, nil
	if wire := upgradeWire(g, plan.Start[0]); wire["c"] != 60 {
		t.Fatalf("новое здание: %v", wire)
	}
}

// Числа — из настоящей партии 10909474 (×4): начало 20.09 15:45:37,
// ответ пришёл через 21 ч 52 мин 39 с, а игровые часы ушли вчетверо дальше.
func TestGameClockSpeedGame(t *testing.T) {
	const start = 1789911937
	received := time.Unix(start, 0).Add(78759 * time.Second)
	c := newGameClock(start, 1790226968768, received)
	if c.scale != 0.25 {
		t.Fatalf("масштаб %v, ждали 0.25", c.scale)
	}
	// Игровое «сейчас» — это наше «сейчас».
	if got := c.real(1790226968768); got.Sub(received).Abs() > 2*time.Second {
		t.Fatalf("игровое сейчас стало %s, ждали %s", got, received)
	}
	// 72 игровых часа железной дороги — 18 настоящих.
	if got := c.duration(72 * time.Hour); got != 18*time.Hour {
		t.Fatalf("72 игровых часа стали %s", got)
	}
}

func TestGameClockNormalGame(t *testing.T) {
	const start = 1789911937
	received := time.Unix(start, 0).Add(10 * time.Hour)
	c := newGameClock(start, received.Add(3*time.Second).UnixMilli(), received)
	if c.scale != 1 {
		t.Fatalf("масштаб %v, ждали 1", c.scale)
	}
	if got := c.real(1790000000000); !got.Equal(time.UnixMilli(1790000000000)) {
		t.Fatalf("в обычной партии время поменялось: %s", got)
	}
	// Без наших часов не гадаем.
	if c := newGameClock(start, 1790226968768, time.Time{}); c.scale != 1 {
		t.Fatal("без часов ответа масштаб не 1")
	}
}
