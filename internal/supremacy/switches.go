package supremacy

// Выключатели зданий: железную дорогу и пункт призыва можно выключить,
// не снося, — они перестают работать и брать содержание. Клиент игры
// делает это функцией toggleUpgradeInProvinces:
//
//	{"@c":"ultshared.action.UltUpdateProvinceAction","mode":0,
//	 "provinceIDs":[484],"upgrade":{"@c":"mu","id":18,"c":60,"e":false},"slot":0}
//
// mode = 0 (PROVINCE), а в upgrade — запись уже стоящего здания с другим
// «e». Клиент шлёт одну запись на все выбранные провинции; мы шлём по
// действию на провинцию: состояние «c» у каждой своё, и подменять его
// чужим незачем. В игру они всё равно уходят одним запросом.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// modeProvince — режим UltUpdateProvinceAction, которым клиент включает
// и выключает здания.
const modeProvince = 0

// Виды выключателей.
const (
	SwitchRailway    = "railway"
	SwitchRecruiting = "recruiting"
)

// SwitchKinds — все выключатели в том порядке, в каком их показывает страница.
var SwitchKinds = []string{SwitchRailway, SwitchRecruiting}

// switchNames — как вид называется в итоге для страницы.
var switchNames = map[string]string{
	SwitchRailway:    "ЖД",
	SwitchRecruiting: "Пункты призыва",
}

// switchKind — к какому выключателю относится здание. Узнаём по картинке:
// у уровней одного здания она общая, а номера у каждого уровня свои.
func switchKind(u Upgrade) string {
	switch {
	case u.Image == "railway":
		return SwitchRailway
	case strings.HasPrefix(u.Image, "recruiting"):
		return SwitchRecruiting
	}
	return ""
}

// SwitchCount — сколько зданий вида в своих провинциях включено и сколько
// выключено.
type SwitchCount struct {
	On, Off int
}

// switchTarget — здание, которое надо переключить.
type switchTarget struct {
	Province  int
	UpgradeID int
	Condition int
}

// Switches — сколько железных дорог и пунктов призыва стоит в своих
// провинциях, включённых и выключенных.
func (g *GameState) Switches() map[string]SwitchCount {
	out := make(map[string]SwitchCount, len(SwitchKinds))
	for _, p := range g.Provinces {
		if p.Owner != g.Me || g.Me <= 0 {
			continue
		}
		for _, id := range p.Built {
			kind := switchKind(g.Upgrades[id])
			if kind == "" {
				continue
			}
			n := out[kind]
			if p.Off[id] {
				n.Off++
			} else {
				n.On++
			}
			out[kind] = n
		}
	}
	return out
}

// switchTargets — здания вида kind в своих провинциях, которые сейчас
// не в положении on.
func (g *GameState) switchTargets(kind string, on bool) []switchTarget {
	var out []switchTarget
	for _, p := range g.Provinces {
		if p.Owner != g.Me || g.Me <= 0 {
			continue
		}
		for _, id := range p.Built {
			if switchKind(g.Upgrades[id]) != kind || !p.Off[id] == on {
				continue
			}
			out = append(out, switchTarget{Province: p.ID, UpgradeID: id, Condition: p.Condition[id]})
		}
	}
	return out
}

// withSwitched — копия состояния, где здания targets стоят в положении on.
// Состояние в кэше общее и только на чтение, поэтому меняется копия:
// сам список провинций и выключатели тронутых провинций.
func (g *GameState) withSwitched(targets []switchTarget, on bool) *GameState {
	byProvince := make(map[int][]int, len(targets))
	for _, t := range targets {
		byProvince[t.Province] = append(byProvince[t.Province], t.UpgradeID)
	}
	cp := *g
	cp.Provinces = make([]Province, len(g.Provinces))
	copy(cp.Provinces, g.Provinces)
	for i, p := range cp.Provinces {
		ids, ok := byProvince[p.ID]
		if !ok {
			continue
		}
		off := make(map[int]bool, len(p.Off)+len(ids))
		for id, v := range p.Off {
			off[id] = v
		}
		for _, id := range ids {
			if on {
				delete(off, id)
			} else {
				off[id] = true
			}
		}
		cp.Provinces[i].Off = off
	}
	return &cp
}

// SwitchBuildings заходит в партию и включает (on) или выключает все
// здания вида kind в своих провинциях. Возвращает итог для страницы.
func (c *Client) SwitchBuildings(ctx context.Context, gameID, kind string, on bool) (string, error) {
	name, known := switchNames[kind]
	if !known {
		return "", fmt.Errorf("неизвестный выключатель %q", kind)
	}
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return "", err
	}

	done, already := "выключили", "выключены"
	if on {
		done, already = "включили", "включены"
	}
	count := state.Switches()[kind]
	targets := state.switchTargets(kind, on)
	if count.On+count.Off == 0 {
		return fmt.Sprintf("%s: в своих провинциях их нет", name), nil
	}
	if len(targets) == 0 {
		return fmt.Sprintf("%s: уже везде %s (%d)", name, already, count.On+count.Off), nil
	}

	actions := make([]any, 0, len(targets))
	for i, t := range targets {
		actions = append(actions, map[string]any{
			"requestID":   "actionReq-" + strconv.Itoa(i+2),
			"@c":          "ultshared.action.UltUpdateProvinceAction",
			"mode":        modeProvince,
			"provinceIDs": []int{t.Province},
			"upgrade":     map[string]any{"@c": "mu", "id": t.UpgradeID, "c": t.Condition, "e": on},
			"slot":        0,
		})
	}
	var res json.RawMessage
	if err := c.gsCall(ctx, acc, gameID, 3, "ultshared.action.UltUpdateGameStateAction", state.Me,
		map[string]any{"actions": actions, "lastCallDuration": 0}, &res); err != nil {
		return "", fmt.Errorf("%s в партии %s: %w", name, gameID, err)
	}

	// Копия в кэше снята до переключения, и страница по ней показала бы
	// прежнее положение. Новый заход ради этого стоил бы ещё одного входа,
	// поэтому правим копию сами.
	c.patchState(gameID, state, state.withSwitched(targets, on))

	c.log.Info("переключили здания", "gameID", gameID, "что", kind, "включены", on, "провинций", len(targets))
	return fmt.Sprintf("%s: %s в %d провинциях из %d", name, done, len(targets), count.On+count.Off), nil
}

// patchState подменяет копию в кэше на исправленную, не трогая время
// съёмки: остальное в ней не новее, чем было. Если за это время кэш успел
// получить другую копию, она свежее нашей правки — её и оставляем.
func (c *Client) patchState(gameID string, was, now *GameState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.states[gameID]; ok && e.state == was {
		e.state = now
	}
}

// Missing — где в своих провинциях нет здания вида kind: ни одного его
// уровня и ни одной идущей стройки. Возвращает первый уровень здания —
// его и ставить — и такие провинции по номеру. ok = false, если игра
// такого здания не знает.
func (g *GameState) Missing(kind string) (first Upgrade, where []Province, ok bool) {
	for _, u := range g.Upgrades {
		if switchKind(u) != kind {
			continue
		}
		if !ok || u.Tier < first.Tier || (u.Tier == first.Tier && u.ID < first.ID) {
			first, ok = u, true
		}
	}
	if !ok || g.Me <= 0 {
		return first, nil, ok
	}
	has := func(p Province) bool {
		for _, id := range p.Built {
			if switchKind(g.Upgrades[id]) == kind {
				return true
			}
		}
		for _, c := range p.Building {
			if switchKind(g.Upgrades[c.UpgradeID]) == kind {
				return true
			}
		}
		return false
	}
	for _, p := range g.Provinces {
		if p.Owner == g.Me && !has(p) {
			where = append(where, p)
		}
	}
	sort.Slice(where, func(i, j int) bool { return where[i].ID < where[j].ID })
	return first, where, true
}

// SwitchKindOf — к какому выключателю относится здание с этим номером;
// пусто — ни к какому.
func (g *GameState) SwitchKindOf(upgradeID int) string {
	return switchKind(g.Upgrades[upgradeID])
}
