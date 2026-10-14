package supremacy

// Сбор войск в город. Рут выбирает свою провинцию, и админка шлёт туда
// армии: разово кнопкой — все, а автопилотом — только появившиеся с его
// прошлого захода.
//
// Приказ тот же, что отдаёт клиент, когда армию ведут мышью (снят с живой
// партии 14.10.2026):
//
//	{"@c":"ultshared.action.UltArmyAction","armies":[
//	  {"@c":"a","id":17000460,"s":1,"o":6,"u":[{"@c":"u","t":2,"s":1,"n":[]}],
//	   "c":[{"@c":"gc","sp":{"x":2186,"y":282},"tp":{"x":2176.92,"y":277.38}},
//	        {"@c":"gc","sp":{"x":2176.92,"y":277.38},"tp":{"x":2150.5,"y":262}},
//	        {"@c":"gc","sp":{"x":2150.5,"y":262},"tp":{"x":2144,"y":297}}],
//	   "au":0,"ag":0,"fm":0}]}
//
// Путь в нём целиком, по отрезкам графа дорог: его прокладываем мы, см.
// FindRoute. Как и у Мейв, команды заменяют очередь армии — прежний марш
// отменяется.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// unitSetLand — род войск «суша» в справочнике юнитов.
const unitSetLand = 1

// rallyForeignCost — во сколько раз дороже идти по чужой земле, чем по
// своей. Сервер считает там скорость втрое ниже (0.19 против 0.54 на
// отрезках живого приказа), и путь в обход по своим провинциям обычно
// быстрее — а заодно не ведёт армию через чужие бои.
const rallyForeignCost = 3

// RallyOrder — армия и путь, которым её поведём.
type RallyOrder struct {
	Army Army
	Path []MapPoint
}

// RallyPlan — кого куда ведём и кого почему не трогаем.
type RallyPlan struct {
	Orders []RallyOrder
	// Пропущенные по причинам: в бою, не сухопутные или на воде, Мейв
	// размещается, уже в городе или идёт туда, не нашлось пути.
	Fighting, NotLand, Deploying, There, NoRoute int
	// Armies — все наши армии в партии сейчас: по ним автопилот узнает
	// в следующий раз, какие появились заново.
	Armies []int64
}

// PlanRally выбирает наши сухопутные армии и прокладывает им путь до
// центра провинции target. pick решает, трогать ли армию вообще: кнопка
// берёт все, автопилот — только новые. skip — армия, которую нельзя
// трогать в этот заход (только что призвавшая пехоту Мейв).
func PlanRally(state *GameState, geo *MapGeometry, target int, pick func(id int64) bool, skip int64) (RallyPlan, error) {
	// Пустой, а не nil список: «армий нет» автопилот тоже запоминает.
	plan := RallyPlan{Armies: []int64{}}
	center, ok := geo.Centers[target]
	if !ok {
		return plan, fmt.Errorf("провинции %d нет на карте суши", target)
	}

	owner := make(map[int]int, len(state.Provinces))
	for _, p := range state.Provinces {
		owner[p.ID] = p.Owner
	}
	cost := func(province int) float64 {
		if owner[province] == state.Me {
			return 1
		}
		return rallyForeignCost
	}

	for _, a := range state.Armies {
		if a.Owner != state.Me {
			continue
		}
		plan.Armies = append(plan.Armies, a.ID)
		if !pick(a.ID) {
			continue
		}
		switch {
		case !landArmy(state, a) || !onLand(geo, a):
			plan.NotLand++
		case a.Fighting:
			plan.Fighting++
		case a.Deploying || a.ID == skip:
			plan.Deploying++
		case a.MarchTo == nil && a.Location == target,
			a.MarchTo != nil && keyOf(*a.MarchTo) == keyOf(center):
			plan.There++
		default:
			path, ok := FindRoute(geo, a.Pos, center, cost)
			if !ok {
				plan.NoRoute++
				continue
			}
			plan.Orders = append(plan.Orders, RallyOrder{Army: a, Path: path})
		}
	}
	return plan, nil
}

// landArmy — в армии только сухопутные войска и герои. Флот и авиация
// ходят не по дорогам суши, а смешанную армию дорогой суши не отправить.
func landArmy(state *GameState, a Army) bool {
	if len(a.Units) == 0 {
		return false
	}
	for _, u := range a.Units {
		if set := state.UnitSets[u.Type]; set != unitSetLand && set != unitSetHero {
			return false
		}
	}
	return true
}

// onLand — армия стоит на суше. Сухопутные войска бывают и на воде —
// когда их везут корабли, — и тогда к дорогам суши они не привязаны.
func onLand(geo *MapGeometry, a Army) bool {
	_, ok := geo.Centers[a.Location]
	return ok
}

// Summary — итог словами, для страницы партии.
func (p RallyPlan) Summary(city string) string {
	var parts []string
	if len(p.Orders) > 0 {
		parts = append(parts, fmt.Sprintf("в %s отправлено армий: %d", city, len(p.Orders)))
	} else {
		parts = append(parts, fmt.Sprintf("в %s отправлять некого", city))
	}
	for _, s := range []struct {
		n    int
		what string
	}{
		{p.There, "уже там или идут туда"},
		{p.Fighting, "в бою"},
		{p.Deploying, "Мейв размещается"},
		{p.NotLand, "флот, авиация или на воде"},
		{p.NoRoute, "нет пути"},
	} {
		if s.n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", s.what, s.n))
		}
	}
	return strings.Join(parts, "; ")
}

// RallyRun — чем кончился заход со сбором войск.
type RallyRun struct {
	Result string
	// Armies — наши армии в партии на момент захода, см. RallyPlan.Armies.
	Armies []int64
}

// RallyArmies заходит в партию и шлёт в провинцию target все наши
// сухопутные армии, которые не в бою и не там уже.
func (c *Client) RallyArmies(ctx context.Context, gameID string, target int, city string) (*RallyRun, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return nil, err
	}
	return c.rally(ctx, acc, gameID, state, 3, target, city, func(int64) bool { return true }, 0)
}

// rally — сбор войск в партии, куда уже зашли.
func (c *Client) rally(ctx context.Context, acc *gameAccess, gameID string, state *GameState, reqID int,
	target int, city string, pick func(int64) bool, skip int64) (*RallyRun, error) {

	geo, err := c.MapGeometry(ctx, state.MapID)
	if err != nil {
		return nil, fmt.Errorf("карта партии %s: %w", gameID, err)
	}
	plan, err := PlanRally(state, geo, target, pick, skip)
	if err != nil {
		return nil, fmt.Errorf("сбор войск в партии %s: %w", gameID, err)
	}
	run := &RallyRun{Armies: plan.Armies}
	if len(plan.Orders) == 0 {
		run.Result = plan.Summary(city)
		return run, nil
	}

	// Каждой армии — своё действие, как приказы складывает очередь клиента;
	// в игру они уходят одним запросом.
	actions := make([]any, 0, len(plan.Orders))
	for i, o := range plan.Orders {
		actions = append(actions, map[string]any{
			"requestID": "actionReq-" + strconv.Itoa(i+2),
			"@c":        "ultshared.action.UltArmyAction",
			"armies":    []any{armyWire(o.Army, gotoCommands(o.Path))},
		})
	}
	var res json.RawMessage
	if err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", state.Me,
		map[string]any{"actions": actions, "lastCallDuration": 0}, &res); err != nil {
		return nil, fmt.Errorf("сбор войск в партии %s: %w", gameID, err)
	}

	run.Result = plan.Summary(city)
	c.log.Info("сбор войск", "gameID", gameID, "город", city, "итог", run.Result)
	return run, nil
}

// AutopilotJob — что автопилот делает в партии за один заход.
type AutopilotJob struct {
	Hero bool
	// RallyTo — куда собирать новые армии; ноль — сбор выключен.
	RallyTo   int
	RallyCity string
	// Known — армии, которые автопилот уже видел. nil — не видел ещё
	// ни одной: первый заход только запоминает, что есть, и никого
	// не трогает — сбор отвечает за новые армии, а не за все.
	Known []int64
}

// AutopilotRun — итог захода по частям: у каждой своё поле на странице.
type AutopilotRun struct {
	Hero, Rally string
	Armies      []int64
}

// RunAutopilot заходит в партию один раз и делает всё включённое:
// заход игра засчитывает как вход, и делать их по одному на кнопку
// незачем. Ошибка — только когда в партию не зашли; отказ отдельной
// части записывается в её итог словами.
func (c *Client) RunAutopilot(ctx context.Context, gameID string, job AutopilotJob) (*AutopilotRun, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return nil, err
	}
	run := &AutopilotRun{}

	// Мейв первой: размещение отменяется маршем, и сбору надо знать,
	// какую армию сейчас не трогать.
	var deploying int64
	if job.Hero {
		result, armyID, err := c.deployInfantry(ctx, acc, gameID, state, 3)
		if err != nil {
			result = err.Error()
		}
		run.Hero, deploying = result, armyID
	}

	if job.RallyTo > 0 {
		pick := newArmies(job.Known)
		rally, err := c.rally(ctx, acc, gameID, state, 4, job.RallyTo, job.RallyCity, pick, deploying)
		switch {
		case err != nil:
			run.Rally = err.Error()
		case job.Known == nil:
			run.Rally = fmt.Sprintf("запомнили армии: %d; новые пойдут в %s", len(rally.Armies), job.RallyCity)
			run.Armies = rally.Armies
		default:
			run.Rally, run.Armies = rally.Result, rally.Armies
		}
	}
	return run, nil
}

// newArmies — отбор «только новые»: армии, которых нет среди известных.
// Пока известных нет вовсе, новых не бывает — см. AutopilotJob.Known.
func newArmies(known []int64) func(int64) bool {
	if known == nil {
		return func(int64) bool { return false }
	}
	seen := make(map[int64]bool, len(known))
	for _, id := range known {
		seen[id] = true
	}
	return func(id int64) bool { return !seen[id] }
}
