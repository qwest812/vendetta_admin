package supremacy

// Навык Мейв «призвать пехоту». В игре он выглядит так: героиня переходит
// в состояние «размещения», замирает на несколько часов, тратит рыбу и
// деньги, а по окончании в её армию добавляется пехота.
//
// Запрос снят с живого нажатия кнопки в браузере (31.08.2026). Уходит он
// не сам по себе, а вложенным в UltUpdateGameStateAction:
//
//	{"@c":"ultshared.action.UltArmyAction","armies":[
//	  {"@c":"a","id":17000885,"s":25,"o":12,
//	   "u":[{"@c":"u","t":50617,"s":1,"n":[]},{"@c":"u","t":2,"s":24}],
//	   "c":[{"@c":"duc"}],"au":0,"ag":0,"fm":0}]}
//
// Важное, что видно из этого запроса: команда `duc` пустая — ни цели, ни
// координат ей не нужно, — и она **заменяет очередь команд армии**. То есть
// призыв отменяет марш: игра ведёт себя так же, когда кнопку жмёт человек.
//
// Армию игра требует вернуть в сокращённом виде: номер, размер, владелец,
// состав по типам юнитов. Поле `n` есть только у героини — отдаём его там
// же, где игра его прислала.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNoHero — в партии нет нашей армии с героем, умеющим призыв пехоты.
// Это не поломка: Мейв появляется не сразу. Кнопка в таком случае
// включает ожидание, а автопилот спрашивает снова на следующем заходе.
var ErrNoHero = errors.New("героя с призывом пехоты в партии пока нет")

// DeployInfantry активирует навык Мейв «призвать пехоту» в партии.
// Возвращает описание сделанного — его показывает страница партии.
func (c *Client) DeployInfantry(ctx context.Context, gameID string) (string, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return "", err
	}
	result, _, err := c.deployInfantry(ctx, acc, gameID, state, 3)
	return result, err
}

// deployInfantry жмёт кнопку в партии, куда уже зашли. Второе значение —
// армия, которая теперь размещается (ноль, если кнопку не жали): марш её
// размещение отменил бы, и сбор войск должен её обойти.
func (c *Client) deployInfantry(ctx context.Context, acc *gameAccess, gameID string,
	state *GameState, reqID int) (string, int64, error) {

	army, hero, ok := state.DeployArmy()
	if !ok {
		return "", 0, fmt.Errorf("партия %s: %w", gameID, ErrNoHero)
	}

	// Повторное нажатие игра принимает молча и таймер не сбрасывает, так что
	// рапортовать об успехе было бы враньём: пока идёт размещение, честнее
	// сказать, сколько осталось.
	if army.Deploying {
		return fmt.Sprintf("%s уже размещается, осталось %s игрового времени",
			hero, gameMinutes(army.DeployLeft)), army.ID, nil
	}

	var res json.RawMessage
	if err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", state.Me,
		map[string]any{
			"actions": []any{map[string]any{
				"requestID": "actionReq-2",
				"@c":        "ultshared.action.UltArmyAction",
				"armies":    []any{armyWire(army, deployCommands())},
			}},
			"lastCallDuration": 0,
		}, &res); err != nil {
		return "", 0, fmt.Errorf("призыв пехоты в партии %s: %w", gameID, err)
	}

	c.log.Info("призвали пехоту", "gameID", gameID, "герой", hero, "армия", army.ID)
	return fmt.Sprintf("%s призвала пехоту (армия %d)", hero, army.ID), army.ID, nil
}

// gameMinutes подписывает остаток по часам игрового сервера. Они идут
// быстрее настоящих во столько раз, во сколько ускорена партия, поэтому
// в реальные минуты это не переводится.
func gameMinutes(d time.Duration) string {
	if d <= 0 {
		return "меньше минуты"
	}
	return fmt.Sprintf("%d мин", int(d.Minutes()))
}

// deployCommands — очередь команд армии с одной пустой командой «призвать
// пехоту»: ни цели, ни координат ей не нужно.
func deployCommands() []any { return []any{map[string]any{"@c": "duc"}} }

// armyWire собирает армию так, как её присылает клиент игры вместе
// с командами: только номер, размер, владелец и состав. Команды заменяют
// собой всю очередь команд армии.
func armyWire(a Army, commands []any) map[string]any {
	units := make([]any, 0, len(a.Units))
	for _, u := range a.Units {
		unit := map[string]any{"@c": "u", "t": u.Type, "s": u.Size}
		if u.HasNames {
			unit["n"] = []any{}
		}
		units = append(units, unit)
	}
	return map[string]any{
		"@c": "a",
		"id": a.ID,
		"s":  a.Size,
		"o":  a.Owner,
		"u":  units,
		"c":  commands,
		"au": 0,
		"ag": 0,
		"fm": 0,
	}
}
