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
	"fmt"
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

// heroDeployCheck — можно ли ставить героя unit в провинцию province.
// Пустая строка — можно; иначе причина словами.
func (g *GameState) heroDeployCheck(unit, province int) (HeroItem, string) {
	choices, deployed := g.HeroChoices()
	var hero *HeroChoice
	for i := range choices {
		if choices[i].Unit == unit {
			hero = &choices[i]
		}
	}
	switch {
	case hero == nil:
		return HeroItem{}, "этого героя нет в инвентаре аккаунта"
	case hero.OnMap:
		return hero.HeroItem, hero.Name + " уже на карте"
	case g.HeroLimit > 0 && deployed >= g.HeroLimit:
		return hero.HeroItem, fmt.Sprintf("на карте уже героев: %d из %d", deployed, g.HeroLimit)
	}
	for _, p := range g.Provinces {
		if p.ID == province {
			if p.Owner != g.Me {
				return hero.HeroItem, p.Name + " — не наша провинция"
			}
			return hero.HeroItem, ""
		}
	}
	return hero.HeroItem, fmt.Sprintf("провинции %d в партии нет", province)
}

// DeployHero ставит героя unit на карту в провинцию province. Возвращает
// описание сделанного — его показывает страница партии.
func (c *Client) DeployHero(ctx context.Context, gameID string, unit, province int, city string) (string, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return "", err
	}
	hero, why := state.heroDeployCheck(unit, province)
	if why != "" {
		return "", fmt.Errorf("%s", why)
	}

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
		return "", fmt.Errorf("размещение героя в партии %s: %w", gameID, err)
	}

	// Отказ сервер присылает тем же полем: всё, что не 1, — «не вышло».
	// Почему, он не говорит; чаще всего это мораль провинции, перезарядка
	// после отзыва или бой в провинции.
	if code, ok := res.ActionResults["actionReq-2"]; !ok || code != 1 {
		return "", fmt.Errorf("игра не разместила %s в %s (ответ %d): проверьте мораль провинции, "+
			"перезарядку героя и нет ли там боя", hero.Name, city, code)
	}
	c.log.Info("герой размещён", "gameID", gameID, "герой", hero.Name, "провинция", province)
	return fmt.Sprintf("%s размещён в %s", hero.Name, city), nil
}
