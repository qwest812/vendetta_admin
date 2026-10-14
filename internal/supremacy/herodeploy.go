package supremacy

// Размещение героя: герой аккаунта — постоянный предмет инвентаря, и
// поставить его на карту значит «использовать» этот предмет в своей
// провинции. Действие снято с живого клиента (14.10.2026):
//
//	{"@c":"UsePremiumAction","premiumID":23047,"amount":1,
//	 "provinces":[481],"armies":[],"researches":[]}
//
// premiumID — предмет героя (у Кангала 23047, при юните 23041). Связь
// «юнит героя → предмет» игра присылает в справочнике партии
// (unitItemIdToPermanentPremiumItemId), а что у аккаунта есть — в его
// инвентаре. Сервер отвечает в actionResults: 1 — принято. Герой
// появляется армией в выбранной провинции, а предмет остаётся.
//
// Размещение идёт приказом: рут ставит «героя — сюда», а админка держит
// приказ, пока условия не сойдутся, и ставит героя сама, см. planHeroOrders.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"Vendetta_admin/internal/domain"
)

// HeroItem — герой аккаунта: тип его юнита, предмет инвентаря, который
// ставит его на карту, и название.
type HeroItem struct {
	Unit int
	Item int
	Name string
}

// HeroChoice — герой для выбора на странице: на карте он или нет.
type HeroChoice struct {
	HeroItem
	OnMap bool
}

// HeroChoices — герои аккаунта и сколько их уже на карте. Герой на карте —
// это наша армия с его юнитом: так считает и клиент игры.
func (g *GameState) HeroChoices() ([]HeroChoice, int) {
	onMap := map[int]bool{}
	for _, a := range g.Armies {
		if a.Owner != g.Me {
			continue
		}
		for _, u := range a.Units {
			onMap[u.Type] = true
		}
	}
	out := make([]HeroChoice, 0, len(g.Heroes))
	deployed := 0
	for _, h := range g.Heroes {
		c := HeroChoice{HeroItem: h, OnMap: onMap[h.Unit]}
		if c.OnMap {
			deployed++
		}
		out = append(out, c)
	}
	return out, deployed
}

// errHeroRefused — игра не приняла размещение. Почему, она не говорит.
var errHeroRefused = errors.New("игра не приняла размещение")

// placeHero шлёт размещение в партию, куда уже зашли.
func (c *Client) placeHero(ctx context.Context, acc *gameAccess, gameID string, state *GameState,
	reqID int, hero HeroItem, province int) error {

	var res struct {
		ActionResults map[string]int `json:"actionResults"`
	}
	if err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", state.Me,
		map[string]any{
			"actions": []any{map[string]any{
				"requestID":  "actionReq-2",
				"@c":         "UsePremiumAction",
				"premiumID":  hero.Item,
				"amount":     1,
				"provinces":  []int{province},
				"armies":     []any{},
				"researches": []any{},
			}},
			"lastCallDuration": 0,
		}, &res); err != nil {
		return fmt.Errorf("размещение героя в партии %s: %w", gameID, err)
	}

	// Отказ сервер присылает тем же полем: всё, что не 1, — «не вышло».
	// Чаще всего это мораль провинции, перезарядка после отзыва или бой.
	if code, ok := res.ActionResults["actionReq-2"]; !ok || code != 1 {
		return errHeroRefused
	}
	c.log.Info("герой размещён", "gameID", gameID, "герой", hero.Name, "провинция", province)
	return nil
}

// heroRetry — через сколько заглянуть снова, когда ждём то, чего
// не предсказать: свободного места в лимите героев, роста морали, конца
// боя или ответа игры после отказа.
const heroRetry = time.Hour

// HeroOrderRun — итог по одному приказу за заход. Next — когда прийти
// снова; нулевое — приказ закрыт: исполнен или исполнить его нельзя.
type HeroOrderRun struct {
	ID     int64
	Result string
	Next   time.Time
}

// heroStep — что делать с приказом в этот заход.
type heroStep struct {
	order domain.HeroPlan
	hero  HeroItem
	place bool
	run   HeroOrderRun
}

// planHeroOrders решает по каждому приказу партии, ставить ли героя
// сейчас, ждать или закрыть. Приказы идут по порядку: свободное место
// в лимите героев достаётся тому, кто поставлен раньше.
//
// Проверяем то, что видно из состояния и справочника: день, лимит,
// героя на карте, свою провинцию, её мораль и бой в ней. Перезарядку
// после отзыва знает только игра — её отказ приходит ответом.
func planHeroOrders(state *GameState, cards map[int]HeroCard, orders []domain.HeroPlan, now time.Time) []heroStep {
	choices, deployed := state.HeroChoices()
	byUnit := map[int]HeroChoice{}
	for _, c := range choices {
		byUnit[c.Unit] = c
	}
	provinces := map[int]Province{}
	for _, p := range state.Provinces {
		provinces[p.ID] = p
	}
	fighting := map[int]bool{}
	for _, a := range state.Armies {
		if a.Fighting {
			fighting[a.Location] = true
		}
	}
	free := -1 // без лимита в состоянии мест не считаем
	if state.HeroLimit > 0 {
		free = state.HeroLimit - deployed
	}
	later := func(why string) HeroOrderRun { return HeroOrderRun{Result: why, Next: now.Add(heroRetry)} }

	steps := make([]heroStep, 0, len(orders))
	for _, o := range orders {
		step := heroStep{order: o, run: HeroOrderRun{ID: o.ID}}
		hero, owned := byUnit[o.Unit]
		province, known := provinces[o.Province]
		card, carded := cards[hero.Item]
		switch {
		case !owned:
			step.run.Result = "этого героя нет в инвентаре аккаунта"
		case hero.OnMap:
			step.run.Result = hero.Name + " уже на карте"
		case !known || province.Owner != state.Me:
			step.run.Result = o.City + " — не наша провинция"
		case carded && card.Day > state.Day:
			next := state.DayStart(card.Day)
			if next.IsZero() || next.Before(now) {
				next = now.Add(heroRetry)
			}
			// Пара минут после смены дня — запас на часы игры и её пересчёт.
			step.run.Result = fmt.Sprintf("ждём %d-й день, идёт %d-й", card.Day, state.Day)
			step.run.Next = next.Add(2 * time.Minute)
		case free == 0:
			step.run = later(fmt.Sprintf("на карте героев %d из %d — ждём свободного места", deployed, state.HeroLimit))
		case carded && province.Morale < float64(card.Morale):
			step.run = later(fmt.Sprintf("мораль %s %.0f%% из нужных %d%%", o.City, province.Morale, card.Morale))
		case fighting[o.Province]:
			step.run = later("в " + o.City + " идёт бой")
		default:
			step.place, step.hero = true, hero.HeroItem
			if free > 0 {
				free--
			}
		}
		step.run.ID = o.ID
		steps = append(steps, step)
	}
	return steps
}

// RunHeroOrders заходит в партию один раз и разбирает все её приказы
// на размещение героев: кого можно — ставит, остальным называет, чего
// ждут и когда заглянуть снова.
func (c *Client) RunHeroOrders(ctx context.Context, gameID string, orders []domain.HeroPlan, now time.Time) ([]HeroOrderRun, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return nil, err
	}
	// Без справочника не знаем дней и порогов морали — тогда решает игра.
	cards, err := c.HeroCards(ctx)
	if err != nil {
		c.log.Warn("справочник героев с сайта", "err", err)
	}

	runs := make([]HeroOrderRun, 0, len(orders))
	reqID := 3 // вход — 1, состояние — 2, дальше наши действия
	for _, step := range planHeroOrders(state, cards, orders, now) {
		if step.place {
			err := c.placeHero(ctx, acc, gameID, state, reqID, step.hero, step.order.Province)
			reqID++
			switch {
			case errors.Is(err, errHeroRefused):
				step.run.Result = "игра отказала (скорее всего перезарядка после отзыва) — попробуем через час"
				step.run.Next = now.Add(heroRetry)
			case err != nil:
				step.run.Result = err.Error()
				step.run.Next = now.Add(heroRetry)
			default:
				step.run.Result = fmt.Sprintf("%s поставлен в %s", step.hero.Name, step.order.City)
			}
		}
		runs = append(runs, step.run)
	}
	return runs, nil
}

// DayStart — когда по настоящим часам начнётся день партии day. Уже
// наступивший день начался в прошлом — тогда отдаём нулевое время.
// Без смены дня в состоянии посчитать нечего: тоже нулевое.
func (g *GameState) DayStart(day int) time.Time {
	if day <= g.Day || g.NextDay.IsZero() || g.DayLength <= 0 {
		return time.Time{}
	}
	return g.NextDay.Add(time.Duration(day-g.Day-1) * g.DayLength)
}
