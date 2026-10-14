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
// Проверки клиента повторяем не все. Свою провинцию, героя не на карте
// и лимит сценария видно из состояния. Порог морали провинции, перезарядку
// после отзыва и атаки на провинцию знает только сайт и сервер — их отказ
// приходит ответом, его и показываем.

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

// Отказ проверки: later — подождать имеет смысл (лимит героев занят,
// день ещё не наступил), иначе — нет (героя нет, провинция чужая, герой
// уже на карте).
type heroRefusal struct {
	why   string
	later bool
}

// heroDeployCheck — можно ли ставить героя unit в провинцию province.
// nil — можно.
func (g *GameState) heroDeployCheck(unit, province int) (HeroItem, *heroRefusal) {
	choices, deployed := g.HeroChoices()
	var hero *HeroChoice
	for i := range choices {
		if choices[i].Unit == unit {
			hero = &choices[i]
		}
	}
	switch {
	case hero == nil:
		return HeroItem{}, &heroRefusal{why: "этого героя нет в инвентаре аккаунта"}
	case hero.OnMap:
		return hero.HeroItem, &heroRefusal{why: hero.Name + " уже на карте"}
	case g.HeroLimit > 0 && deployed >= g.HeroLimit:
		return hero.HeroItem, &heroRefusal{
			why: fmt.Sprintf("на карте уже героев: %d из %d", deployed, g.HeroLimit), later: true}
	}
	for _, p := range g.Provinces {
		if p.ID == province {
			if p.Owner != g.Me {
				return hero.HeroItem, &heroRefusal{why: p.Name + " — не наша провинция"}
			}
			return hero.HeroItem, nil
		}
	}
	return hero.HeroItem, &heroRefusal{why: fmt.Sprintf("провинции %d в партии нет", province)}
}

// errHeroRefused — игра не приняла размещение. Почему, она не говорит.
var errHeroRefused = errors.New("игра не приняла размещение")

// DeployHero ставит героя unit на карту в провинцию province. Возвращает
// описание сделанного — его показывает страница партии.
func (c *Client) DeployHero(ctx context.Context, gameID string, unit, province int, city string) (string, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return "", err
	}
	hero, refusal := state.heroDeployCheck(unit, province)
	if refusal != nil {
		return "", errors.New(refusal.why)
	}
	// Рано ли, знает только справочник сайта; не ответил он — пусть
	// решает игра.
	if cards, err := c.HeroCards(ctx); err == nil {
		if day := cards[hero.Item].Day; day > state.Day {
			return "", fmt.Errorf("%s доступен с %d-го дня, а идёт %d-й — запланируйте размещение",
				hero.Name, day, state.Day)
		}
	}
	if err := c.placeHero(ctx, acc, gameID, state, hero, province); err != nil {
		if errors.Is(err, errHeroRefused) {
			return "", fmt.Errorf("игра не разместила %s в %s: проверьте мораль провинции, "+
				"перезарядку героя и нет ли там боя", hero.Name, city)
		}
		return "", err
	}
	return fmt.Sprintf("%s размещён в %s", hero.Name, city), nil
}

// placeHero шлёт размещение в партию, куда уже зашли.
func (c *Client) placeHero(ctx context.Context, acc *gameAccess, gameID string, state *GameState,
	hero HeroItem, province int) error {

	var res struct {
		ActionResults map[string]int `json:"actionResults"`
	}
	if err := c.gsCall(ctx, acc, gameID, 3, "ultshared.action.UltUpdateGameStateAction", state.Me,
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

// heroRetry — через сколько пробовать снова, когда игра отказала или
// лимит героев занят: мораль растёт, бои кончаются, герои гибнут.
const heroRetry = time.Hour

// HeroPlanRun — итог захода по плану размещения. Next — когда прийти
// снова; нулевое — план исполнен или исполнить его нельзя.
type HeroPlanRun struct {
	Result string
	Next   time.Time
}

// RunHeroPlan заходит в партию и ставит героя по плану, если пора.
// Не пора — называет, когда придёт нужный день; отказ, который может
// пройти, — повторит через час.
func (c *Client) RunHeroPlan(ctx context.Context, p domain.HeroPlan, now time.Time) (*HeroPlanRun, error) {
	acc, state, err := c.enter(ctx, p.GameID)
	if err != nil {
		return nil, err
	}
	if state.Day < p.Day {
		next := state.DayStart(p.Day)
		if next.IsZero() || next.Before(now) {
			next = now.Add(heroRetry)
		}
		// Пара минут после смены дня — запас на часы игры и на её пересчёт.
		return &HeroPlanRun{Result: fmt.Sprintf("ждём %d-й день, идёт %d-й", p.Day, state.Day),
			Next: next.Add(2 * time.Minute)}, nil
	}

	hero, refusal := state.heroDeployCheck(p.Unit, p.Province)
	if refusal != nil {
		run := &HeroPlanRun{Result: refusal.why}
		if refusal.later {
			run.Next = now.Add(heroRetry)
		}
		return run, nil
	}
	if err := c.placeHero(ctx, acc, p.GameID, state, hero, p.Province); err != nil {
		if errors.Is(err, errHeroRefused) {
			return &HeroPlanRun{Result: "игра отказала (мораль, перезарядка или бой) — попробуем через час",
				Next: now.Add(heroRetry)}, nil
		}
		return nil, err
	}
	return &HeroPlanRun{Result: fmt.Sprintf("%s размещён в %s", hero.Name, p.City)}, nil
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
