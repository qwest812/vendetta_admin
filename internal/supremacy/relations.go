package supremacy

// Право прохода ботам. В игре отношение одностороннее: его выдаёт тот, кто
// выдаёт, согласия второй стороны не нужно, а армии того, кому выдали,
// получают проход через наши земли. Боту это ничего не стоит, а нам —
// дорогу через его страну наоборот не даёт: это его отношение к нам.
//
// Действие то же, что шлёт окно дипломатии клиента и что раздаёт
// расширение (см. extension/README.md, «Как меняются отношения»):
//
//	{"@c":"ultshared.action.UltChangeRelationAction",
//	 "playerB":[12],"relationType":"Aw=="}
//
// playerB — номера игроков списком, relationType — одно отношение одним
// байтом в base64. Как сервер разберёт список длиннее одного, не
// проверено, поэтому каждому боту — своё действие; в запрос они уходят
// все вместе, как их складывает очередь клиента.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Шкала отношений клиента: −10 бунт, −2 война, −1 перемирие, 0 торговое
// эмбарго, 1 мир, 2 пакт о ненападении, 3 право прохода, 4 общая карта,
// 5 взаимная защита, 6 общая разведка, 7 командование армиями. Каждая
// ступень выше права прохода включает его в себя.
const RelationRightOfWay = 3

// nativeBot — страна компьютерная с начала партии: computerPlayer, нет
// номера на сайте и nativeComputer. Последнего поля у сервера может не
// быть — тогда хватает первых двух. Промахнуться мимо бота не страшно,
// задеть человека — страшно, поэтому номер на сайте отсекает всех людей.
func nativeBot(computer bool, native *bool, siteUserID int64) bool {
	if !computer || siteUserID > 0 {
		return false
	}
	return native == nil || *native
}

// relationsOf разбирает строку neighborRelations — наше отношение к каждому
// игроку. Это java-мапа: рядом с номерами лежит служебный «@c», и
// значения бывают не числами. Всё непонятное пропускаем.
func relationsOf(raw json.RawMessage) map[int]int {
	if len(raw) == 0 {
		return nil
	}
	var row map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil {
		return nil
	}
	out := make(map[int]int, len(row))
	for k, v := range row {
		id, err := strconv.Atoi(k)
		if err != nil || id <= 0 {
			continue
		}
		var rel int
		if json.Unmarshal(v, &rel) == nil {
			out[id] = rel
		}
	}
	return out
}

// RightOfWayTargets — боты, которым право прохода ещё не выдано: живые
// компьютерные страны, у кого наше отношение ниже права прохода. Выше —
// общая карта и дальше — право прохода уже включает, и понижать его
// незачем. Отношения, о котором игра не сказала, считаем невыданным.
func (g *GameState) RightOfWayTargets() (targets []Player, total int) {
	for id, p := range g.Players {
		if id == g.Me || !p.NativeAI || p.Defeated || p.Retired {
			continue
		}
		total++
		if rel, ok := g.Relations[id]; ok && rel >= RelationRightOfWay {
			continue
		}
		targets = append(targets, p)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets, total
}

// GrantRightOfWay заходит в партию и выдаёт право прохода всем ботам,
// у кого его ещё нет. Возвращает описание сделанного для страницы.
func (c *Client) GrantRightOfWay(ctx context.Context, gameID string) (string, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return "", err
	}

	targets, total := state.RightOfWayTargets()
	if total == 0 {
		return "ботов в партии нет", nil
	}
	if len(targets) == 0 {
		return fmt.Sprintf("право прохода уже есть у всех ботов (%d)", total), nil
	}

	relation := base64.StdEncoding.EncodeToString([]byte{RelationRightOfWay})
	actions := make([]any, 0, len(targets))
	names := make([]string, 0, len(targets))
	for i, p := range targets {
		actions = append(actions, map[string]any{
			"requestID":    "actionReq-" + strconv.Itoa(i+2),
			"@c":           "ultshared.action.UltChangeRelationAction",
			"playerB":      []int{p.ID},
			"relationType": relation,
		})
		names = append(names, p.Nation)
	}

	var res json.RawMessage
	if err := c.gsCall(ctx, acc, gameID, 3, "ultshared.action.UltUpdateGameStateAction", state.Me,
		map[string]any{"actions": actions, "lastCallDuration": 0}, &res); err != nil {
		return "", fmt.Errorf("право прохода в партии %s: %w", gameID, err)
	}

	c.log.Info("выдали право прохода ботам", "gameID", gameID, "ботов", len(targets), "страны", names)
	return fmt.Sprintf("право прохода выдано ботам: %d из %d (%s)",
		len(targets), total, strings.Join(names, ", ")), nil
}
