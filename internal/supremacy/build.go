package supremacy

// Очередь строительства: админка держит на каждую провинцию список зданий
// и ставит следующее, как только освобождается слот.
//
// Стройка в игре — то же действие, каким клиент ставит здание руками:
//
//	{"@c":"ultshared.action.UltUpdateProvinceAction","mode":1,
//	 "provinceIDs":[51],"upgrade":{"@c":"mu","id":16},"slot":0}
//
// mode = 1 (UPGRADE) означает «построить здание»; провинции игра принимает
// списком, но мы ставим по одной — так понятнее, что именно не получилось.
//
// Живого запроса на начало стройки в перехвате не было: поля собраны по
// классу действия в клиенте (UltUpdateProvinceAction) и по тому, как здание
// выглядит в состоянии партии («mu» с номером). Если игра откажет, править
// надо здесь — больше это действие нигде не собирается.
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
)

const (
	// modeUpgrade — режим UltUpdateProvinceAction «построить здание».
	modeUpgrade = 1

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
func buildList(raw json.RawMessage, single *buildWire) []Construction {
	var out []Construction
	add := func(w *buildWire) {
		if w == nil || w.Upgrade.ID <= 0 {
			return
		}
		out = append(out, Construction{UpgradeID: w.Upgrade.ID, Ends: time.UnixMilli(w.Ends)})
	}

	var wrapper []json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper) >= 2 {
		var entries []*buildWire
		if err := json.Unmarshal(wrapper[1], &entries); err == nil {
			for _, e := range entries {
				add(e)
			}
			return out
		}
	}
	add(single)
	return out
}

// BuildOrder — что поставить в стройку в этот заход.
type BuildOrder struct {
	ProvinceID int
	UpgradeID  int
	Province   string
	Upgrade    string
}

// BuildPlan — решение одного захода: что ставим сейчас, когда приходить
// снова и что сказать человеку. Next нулевой означает «приходить незачем»:
// в партии не осталось ничего, чего стоило бы ждать.
type BuildPlan struct {
	Start []BuildOrder
	Next  time.Time
	Notes []string
}

// PlanBuilds решает, что делать в партии, по её состоянию и очередям.
// Ничего не отправляет и в сеть не ходит — отдельно от захода, чтобы
// правила очереди можно было проверить тестом.
//
// queue — очередь на провинцию: номера зданий по порядку. Первое в списке
// и есть то, что мы хотим построить следующим.
func PlanBuilds(g *GameState, queue map[int][]int, now time.Time) BuildPlan {
	var plan BuildPlan

	// Ждать надо самого раннего события из всех провинций: окончания
	// стройки или накопления ресурсов.
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

	// Порядок обхода — по номеру провинции: заход должен читаться одинаково
	// от раза к разу, а в карте порядка нет.
	ids := make([]int, 0, len(queue))
	for id := range queue {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	for _, id := range ids {
		want := queue[id]
		if len(want) == 0 {
			continue
		}
		province, ok := g.Province(id)
		if !ok {
			note("провинции %d нет на карте — очередь стоит", id)
			continue
		}
		name := province.Name
		if name == "" {
			name = fmt.Sprintf("провинция %d", id)
		}
		// Чужую провинцию не трогаем и ждать её не будем: очередь
		// останется на месте, но заход из-за неё не назначается.
		if province.Owner != g.Me {
			note("%s больше не наша — очередь стоит", name)
			continue
		}
		// Слот занят — неважно, нашей стройкой или начатой руками:
		// дождёмся конца и поставим своё.
		if !province.Free() {
			wake(province.BuildsUntil().Add(buildAfter))
			continue
		}

		upgrade, known := g.Upgrades[want[0]]
		if !known {
			note("%s: игра не знает здание %d — уберите его из очереди", name, want[0])
			continue
		}
		at, lack, ok := affordable(g, upgrade, now)
		if !ok {
			note("%s: на «%s» не хватает ресурса «%s», и он не копится", name, upgrade.Name, lack)
			continue
		}
		if at.After(now) {
			note("%s: на «%s» не хватает ресурса «%s», ждём", name, upgrade.Name, lack)
			wake(at.Add(buildAfter))
			continue
		}
		plan.Start = append(plan.Start, BuildOrder{
			ProvinceID: id, UpgradeID: upgrade.ID,
			Province: name, Upgrade: upgrade.Name,
		})
	}

	plan.Next = clampVisit(plan.Next, now)
	return plan
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

// affordable — когда на здание хватит ресурсов. Возвращает время (now,
// если хватает уже), название самого долгого ресурса и признак того, что
// он вообще накопится. Про ресурс, которого игра не назвала, считаем,
// что он есть: молчание — не повод стопорить очередь навсегда.
func affordable(g *GameState, u Upgrade, now time.Time) (time.Time, string, bool) {
	at, lack := now, ""
	for id, need := range u.Cost {
		res, known := g.Resources[id]
		if !known || need <= 0 {
			continue
		}
		enough, ever := res.Enough(need, now)
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
func (c *Client) RunBuildQueue(ctx context.Context, gameID string, queue map[int][]int) (*BuildRun, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return nil, err
	}

	plan := PlanBuilds(state, queue, time.Now())
	run := &BuildRun{Next: plan.Next, Notes: plan.Notes}

	// Номер запроса продолжает тот, на котором кончился заход: вход — 1,
	// состояние — 2, дальше наши действия.
	reqID := 3
	for _, order := range plan.Start {
		var res json.RawMessage
		err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", state.Me,
			map[string]any{
				"actions": []any{map[string]any{
					"requestID":   fmt.Sprintf("actionReq-%d", reqID),
					"@c":          "ultshared.action.UltUpdateProvinceAction",
					"mode":        modeUpgrade,
					"provinceIDs": []int{order.ProvinceID},
					"upgrade":     map[string]any{"@c": "mu", "id": order.UpgradeID},
					"slot":        0,
				}},
				"lastCallDuration": 0,
			}, &res)
		reqID++
		if err != nil {
			// Отказ по одной провинции не повод бросать остальные: сырья
			// могло не хватить именно на неё.
			run.Failed = append(run.Failed,
				fmt.Sprintf("%s: «%s» не поставилось (%v)", order.Province, order.Upgrade, err))
			c.log.Warn("очередь строительства", "gameID", gameID,
				"провинция", order.ProvinceID, "здание", order.UpgradeID, "err", err)
			continue
		}
		run.Started = append(run.Started, order)
		c.log.Info("поставили здание", "gameID", gameID,
			"провинция", order.Province, "здание", order.Upgrade)
	}

	run.Next = nextVisit(plan.Next, len(run.Started), len(run.Failed), time.Now())
	return run, nil
}
