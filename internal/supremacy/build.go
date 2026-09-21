package supremacy

// Очередь строительства: админка держит на каждую провинцию список зданий
// и ставит следующее, как только освобождается слот.
//
// Стройка в игре — то же действие, каким клиент ставит здание руками:
//
//	{"@c":"ultshared.action.UltUpdateProvinceAction","mode":1,
//	 "provinceIDs":[484],"upgrade":{"@c":"mu","id":18,"c":16,"e":true},"slot":0}
//
// mode = 1 (UPGRADE) означает «построить здание»; провинции игра принимает
// списком, но мы ставим по одной — так понятнее, что именно не получилось.
// upgrade — запись здания так, как её сериализует клиент (toJSON у «mu»):
// с состоянием c. Проверено вживую 21.09 на 10909474: недостроенную
// железную дорогу (c = 16 из bc = 60) игра приняла и достраивает.
// Если игра откажет, править надо здесь — больше это действие нигде
// не собирается.
//
// Когда приходить снова, партия рассказывает сама: у идущей стройки есть
// время окончания, у ресурсов — запас и прирост в секунду. Поэтому заход
// назначается не «раз в час», а к тому мигу, когда будет что делать.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"Vendetta_admin/internal/domain"
)

const (
	// modeUpgrade — режим UltUpdateProvinceAction «построить здание»,
	// modeUnit — «произвести войско» (SPECIAL_UNIT у клиента).
	modeUpgrade = 1
	modeUnit    = 2

	// buildAfter — насколько позже окончания стройки приходить. Минуты
	// хватает, чтобы игровой сервер успел закрыть стройку; приходить
	// секунда в секунду смысла нет.
	buildAfter = time.Minute

	// buildRecheck — через сколько перечитать партию после того, как мы
	// что-то поставили. Настоящее время окончания называет только сама
	// игра, а справочное (bt) не учитывает ни скорость партии, ни боевой
	// дух провинции, поэтому не гадаем, а переспрашиваем.
	buildRecheck = 2 * time.Minute

	// buildRefused — через сколько пробовать здание, которое игра
	// не приняла. Чаще незачем: чаще всего это здание, которому ещё
	// не пришёл срок, и ждать его приходится днями.
	buildRefused = time.Hour

	// buildSoonest и buildLatest — границы паузы до следующего захода.
	// Нижняя бережёт партию от частых входов, верхняя не даёт воркеру
	// заснуть навсегда, если считать оказалось нечего.
	buildSoonest = time.Minute
	buildLatest  = 6 * time.Hour
)

// buildWire — стройка, как её присылает игра: здание, время окончания (t)
// и время начала (s), оба в миллисекундах.
type buildWire struct {
	Upgrade struct {
		ID int `json:"id"`
	} `json:"u"`
	Ends  int64 `json:"t"`
	Start int64 `json:"s"`
}

// buildList разбирает идущие стройки провинции. Список игра шлёт особым
// видом — ["ultshared.UltProductionList",[запись, null, …]], — и пустые
// места в нём означают свободные слоты. Нет списка — берём одиночную
// запись bi: в ней лежит та же первая стройка.
func buildList(raw json.RawMessage, single *buildWire, clock gameClock) []Construction {
	var out []Construction
	add := func(w *buildWire) {
		if w == nil || w.Upgrade.ID <= 0 {
			return
		}
		out = append(out, Construction{UpgradeID: w.Upgrade.ID, Ends: clock.real(w.Ends)})
	}

	if entries, ok := productionEntries(raw); ok {
		for _, e := range entries {
			var w *buildWire
			if json.Unmarshal(e, &w) == nil {
				add(w)
			}
		}
		return out
	}
	add(single)
	return out
}

// productionEntries разворачивает особый список игры
// ["ultshared.UltProductionList",[запись, null, …]] в записи. Ложь —
// списка нет или он другого вида.
func productionEntries(raw json.RawMessage) ([]json.RawMessage, bool) {
	var wrapper []json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil || len(wrapper) < 2 {
		return nil, false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(wrapper[1], &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// unitWire — войско, как оно лежит в заказе и в производстве: «su»
// с юнитом внутри, у юнита t — номер типа.
type unitWire struct {
	Unit struct {
		Type int `json:"t"`
	} `json:"unit"`
}

// productionList разбирает идущее производство войск провинции (prs).
// Вид тот же, что у строек, только в u лежит не здание, а «su».
func productionList(raw json.RawMessage, clock gameClock) []Production {
	entries, _ := productionEntries(raw)
	var out []Production
	for _, e := range entries {
		var w *struct {
			U    unitWire `json:"u"`
			Ends int64    `json:"t"`
		}
		if json.Unmarshal(e, &w) != nil || w == nil || w.U.Unit.Type <= 0 {
			continue
		}
		out = append(out, Production{UnitTypeID: w.U.Unit.Type, Ends: clock.real(w.Ends)})
	}
	return out
}

// possibleUnits — что провинция может производить, по номеру типа.
// Запись сохраняется целиком: в заказ её отправляют как есть, так же
// делает клиент игры.
func possibleUnits(list []json.RawMessage) map[int]json.RawMessage {
	out := make(map[int]json.RawMessage, len(list))
	for _, raw := range list {
		var w unitWire
		if json.Unmarshal(raw, &w) == nil && w.Unit.Type > 0 {
			out[w.Unit.Type] = raw
		}
	}
	return out
}

// upgradeWire — здание в действии постройки, как его шлёт клиент игры:
// у недостроенного — его нынешнее состояние, у нового — один уровень.
// Одного номера игре мало: недостроенную дорогу в Сурселе (16 из 60)
// она приняла только в таком виде.
func upgradeWire(g *GameState, order BuildOrder) map[string]any {
	condition := g.Upgrades[order.UpgradeID].BuildCondition
	if p, ok := g.Province(order.ProvinceID); ok {
		if c, ok := p.Condition[order.UpgradeID]; ok {
			condition = c
		}
	}
	return map[string]any{"@c": "mu", "id": order.UpgradeID, "c": condition, "e": true}
}

// BuildOrder — что поставить в стройку в этот заход.
type BuildOrder struct {
	// Kind — domain.QueueBuilding или domain.QueueUnit.
	Kind       string
	ProvinceID int
	// UpgradeID — номер здания или типа войск.
	UpgradeID int
	Province  string
	Upgrade   string
}

// BuildPlan — решение одного захода: что ставим сейчас, когда приходить
// снова и что сказать человеку. Next нулевой означает «приходить незачем»:
// в партии не осталось ничего, чего стоило бы ждать.
type BuildPlan struct {
	Start []BuildOrder
	Next  time.Time
	Notes []string
}

// PlanBuilds — план только по очереди зданий. Оставлен ради краткости
// тестов: воркер зовёт PlanQueues.
func PlanBuilds(g *GameState, queue map[int][]int, now time.Time) BuildPlan {
	return PlanQueues(g, domain.BuildQueues{Buildings: queue}, now)
}

// PlanQueues решает, что делать в партии, по её состоянию и очередям.
// Ничего не отправляет и в сеть не ходит — отдельно от захода, чтобы
// правила очереди можно было проверить тестом.
//
// В очередях на каждую провинцию номера по порядку; первый и есть то,
// что ставим следующим. Здания и войска идут каждый в свой слот, но сырьё
// у них общее: запас уменьшается после каждого заказа, иначе вторая
// провинция полезла бы в те же деньги, что уже отданы первой. Здания
// идут первыми — они дольше и от них зависит, какие войска вообще доступны.
func PlanQueues(g *GameState, q domain.BuildQueues, now time.Time) BuildPlan {
	var plan BuildPlan
	spent := map[int]float64{}

	// Ждать надо самого раннего события из всех провинций: окончания
	// стройки или производства, накопления ресурсов.
	wake := func(at time.Time) {
		if at.IsZero() {
			return
		}
		if plan.Next.IsZero() || at.Before(plan.Next) {
			plan.Next = at
		}
	}
	note := func(format string, args ...any) {
		plan.Notes = append(plan.Notes, fmt.Sprintf(format, args...))
	}
	// pay — хватит ли сырья сейчас. Не хватит — заметка и срок, когда
	// хватит; хватит — сырьё списывается с общего запаса этого захода.
	pay := func(name, what string, cost map[int]float64) bool {
		at, lack, ok := affordable(g, cost, spent, now)
		if !ok {
			note("%s: на «%s» не хватает ресурса «%s», и он не копится", name, what, lack)
			return false
		}
		if at.After(now) {
			note("%s: на «%s» не хватает ресурса «%s», ждём", name, what, lack)
			wake(at.Add(buildAfter))
			return false
		}
		for id, need := range cost {
			spent[id] += need
		}
		return true
	}

	// province — своя провинция из очереди; ложь, если с ней делать нечего.
	province := func(id int) (Province, string, bool) {
		p, ok := g.Province(id)
		if !ok {
			note("провинции %d нет на карте — очередь стоит", id)
			return p, "", false
		}
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("провинция %d", id)
		}
		// Чужую провинцию не трогаем и ждать её не будем: очередь
		// останется на месте, но заход из-за неё не назначается.
		if p.Owner != g.Me {
			note("%s больше не наша — очередь стоит", name)
			return p, name, false
		}
		return p, name, true
	}

	for _, id := range sortedIDs(q.Buildings) {
		want := q.Buildings[id]
		p, name, ok := province(id)
		if !ok {
			continue
		}
		// Слот занят — неважно, нашей стройкой или начатой руками:
		// дождёмся конца и поставим своё.
		if !p.Free() {
			wake(p.BuildsUntil().Add(buildAfter))
			continue
		}
		upgrade, known := g.Upgrades[want[0]]
		if !known {
			note("%s: игра не знает здание %d — уберите его из очереди", name, want[0])
			continue
		}
		// Стоит целиком — второй раз его не поставить: следующий уровень
		// у игры другое здание. Недостроенное или повреждённое, напротив,
		// ставится снова: так игра его и достраивает.
		if g.Finished(p, upgrade.ID) {
			note("%s: «%s» уже построено — уберите из очереди", name, upgrade.Name)
			continue
		}
		if !pay(name, upgrade.Name, upgrade.Cost) {
			continue
		}
		plan.Start = append(plan.Start, BuildOrder{
			Kind: domain.QueueBuilding, ProvinceID: id, UpgradeID: upgrade.ID,
			Province: name, Upgrade: upgrade.Name,
		})
	}

	for _, id := range sortedIDs(q.Units) {
		want := q.Units[id]
		p, name, ok := province(id)
		if !ok {
			continue
		}
		if !p.ProdFree() {
			wake(p.ProducesUntil().Add(buildAfter))
			continue
		}
		unit, known := g.Units[want[0]]
		if !known {
			note("%s: игра не даёт заказать войско %d — уберите его из очереди", name, want[0])
			continue
		}
		// Может ли провинция его производить, решает игра: нужное здание,
		// исследование. Ждать ради этого не назначаем — здание, если оно
		// в очереди строительства, само разбудит воркер концом стройки.
		if _, can := p.CanProduce[unit.ID]; !can {
			note("%s: «%s» здесь пока не производится — не хватает здания", name, unit.Name)
			continue
		}
		if !pay(name, unit.Name, unit.Cost) {
			continue
		}
		plan.Start = append(plan.Start, BuildOrder{
			Kind: domain.QueueUnit, ProvinceID: id, UpgradeID: unit.ID,
			Province: name, Upgrade: unit.Name,
		})
	}

	plan.Next = clampVisit(plan.Next, now)
	return plan
}

// sortedIDs — провинции очереди по номеру: заход должен читаться
// одинаково от раза к разу, а в карте порядка нет. Пустые пропускаются.
func sortedIDs(queue map[int][]int) []int {
	ids := make([]int, 0, len(queue))
	for id, want := range queue {
		if len(want) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// clampVisit зажимает срок захода с обеих сторон: слишком скоро
// возвращаться в партию нельзя — заход игра засчитывает как вход, —
// а заснуть навсегда тем более. Нулевой срок означает «приходить незачем»
// и таким и остаётся.
func clampVisit(at, now time.Time) time.Time {
	if at.IsZero() {
		return at
	}
	if soonest := now.Add(buildSoonest); at.Before(soonest) {
		return soonest
	}
	if latest := now.Add(buildLatest); at.After(latest) {
		return latest
	}
	return at
}

// nextVisit — когда возвращаться после захода. events — ближайшее событие
// самой партии (конец стройки, накопление ресурсов); к нему добавляется
// то, что стало известно по ходу захода:
//
//   - что-то встало в стройку — надо вернуться за настоящим временем
//     окончания, его называет только игра;
//   - всё отказано — возвращаться через две минуты незачем: отказ так
//     быстро не меняется. Так бывает у здания, которому ещё не пришёл
//     срок: железная дорога откроется на третий день, и до тех пор игра
//     будет отказывать. Ходить ради этого каждые две минуты нельзя.
//
// Событие партии всегда важнее: если в соседней провинции через десять
// минут кончается стройка, мы всё равно придём туда — и заодно попробуем
// отказанное ещё раз.
func nextVisit(events time.Time, started, failed int, now time.Time) time.Time {
	extra := time.Time{}
	switch {
	case started > 0:
		extra = now.Add(buildRecheck)
	case failed > 0:
		extra = now.Add(buildRefused)
	}
	switch {
	case events.IsZero():
		events = extra
	case !extra.IsZero() && extra.Before(events):
		events = extra
	}
	return clampVisit(events, now)
}

// affordable — когда хватит ресурсов на cost сверх уже потраченного
// в этом заходе (spent). Возвращает время (now, если хватает уже),
// название самого долгого ресурса и признак того, что он вообще
// накопится. Про ресурс, которого игра не назвала, считаем, что он есть:
// молчание — не повод стопорить очередь навсегда.
func affordable(g *GameState, cost, spent map[int]float64, now time.Time) (time.Time, string, bool) {
	at, lack := now, ""
	for id, need := range cost {
		res, known := g.Resources[id]
		if !known || need <= 0 {
			continue
		}
		enough, ever := res.Enough(need+spent[id], now)
		if !ever {
			return time.Time{}, res.Name, false
		}
		if enough.After(at) {
			at, lack = enough, res.Name
		}
	}
	return at, lack, true
}

// BuildRun — чем кончился заход воркера в партию.
type BuildRun struct {
	// Started — что удалось поставить: эти записи из очереди уходят.
	Started []BuildOrder
	// Failed — что игра не приняла, вместе с её отказом. Записи остаются
	// в очереди: отказ бывает и временным.
	Failed []string
	// Next — когда приходить снова.
	Next time.Time
	// Notes — то же, что в плане: почему очередь стоит.
	Notes []string
}

// RunBuildQueue — один заход в партию ради очереди строительства. Заход
// игра засчитывает как вход, поэтому за него делается всё сразу: читаем
// состояние, ставим что можем и решаем, когда прийти снова.
func (c *Client) RunBuildQueue(ctx context.Context, gameID string, queue domain.BuildQueues) (*BuildRun, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return nil, err
	}

	plan := PlanQueues(state, queue, time.Now())
	run := &BuildRun{Next: plan.Next, Notes: plan.Notes}

	// Номер запроса продолжает тот, на котором кончился заход: вход — 1,
	// состояние — 2, дальше наши действия.
	reqID := 3
	for _, order := range plan.Start {
		// Здание и войско заказываются одним действием, разница в режиме
		// и в том, что лежит в upgrade: у войска — запись «su» из списка
		// провинции, как её шлёт клиент игры.
		mode, upgrade := modeUpgrade, any(upgradeWire(state, order))
		if order.Kind == domain.QueueUnit {
			p, _ := state.Province(order.ProvinceID)
			mode, upgrade = modeUnit, p.CanProduce[order.UpgradeID]
		}
		var res json.RawMessage
		err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", state.Me,
			map[string]any{
				"actions": []any{map[string]any{
					"requestID":   fmt.Sprintf("actionReq-%d", reqID),
					"@c":          "ultshared.action.UltUpdateProvinceAction",
					"mode":        mode,
					"provinceIDs": []int{order.ProvinceID},
					"upgrade":     upgrade,
					"slot":        0,
				}},
				"lastCallDuration": 0,
			}, &res)
		reqID++
		if err != nil {
			// Отказ по одной провинции не повод бросать остальные: сырья
			// могло не хватить именно на неё.
			run.Failed = append(run.Failed,
				fmt.Sprintf("%s: «%s» не принято (%v)", order.Province, order.Upgrade, err))
			c.log.Warn("очередь строительства", "gameID", gameID, "вид", order.Kind,
				"провинция", order.ProvinceID, "номер", order.UpgradeID, "err", err)
			continue
		}
		run.Started = append(run.Started, order)
		msg := "поставили здание"
		if order.Kind == domain.QueueUnit {
			msg = "заказали войско"
		}
		c.log.Info(msg, "gameID", gameID, "провинция", order.Province, "что", order.Upgrade)
	}

	run.Next = nextVisit(plan.Next, len(run.Started), len(run.Failed), time.Now())
	return run, nil
}
