package supremacy

// Экономика игроков партии: сколько каждый добывает в сутки и сколько
// у него уходит на содержание. Свои запасы игра присылает готовыми, а про
// чужих — только то, из чего их можно сложить: добычу каждой провинции,
// население, здания и армии. Складываем так же, как клиент игры:
//
//   - добыча — «arp» провинции, игра считает его сама со всеми бонусами;
//   - потребление провинции — floor(население × resourceConsumption)
//     × множитель её состояния (Province.getConsumption у клиента);
//   - содержание здания — «dc» × состояние / полное состояние, плюс
//     содержание всех уровней, которые оно заменило
//     (UpgradeType.getDailyDeltaFor у клиента);
//   - содержание войск — dailyCosts типа × численность.
//
// Чужие армии видны не все — только те, что не спрятаны туманом, поэтому
// содержание войск у других — нижняя граница. Свою строку можно сверить
// с настоящим приростом из профиля ресурсов: см. Economy.Actual.

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
)

// Economy — суточный баланс одного игрока по ресурсам. Все суммы —
// в игровых сутках: так их считает и показывает сама игра.
type Economy struct {
	Player    Player
	Provinces int
	Output    map[int]float64 // добыча провинций
	Populace  map[int]float64 // потребление населения
	Buildings map[int]float64 // содержание зданий
	Army      map[int]float64 // содержание видимых войск
	// Actual — настоящий прирост в сутки из профиля ресурсов. Он есть
	// только у нас самих и нужен для сверки расчёта.
	Actual map[int]float64
}

// Net — итог за сутки по ресурсу: добыча минус всё содержание.
func (e Economy) Net(res int) float64 {
	return e.Output[res] - e.Populace[res] - e.Buildings[res] - e.Army[res]
}

// Economies — баланс всех игроков, у кого есть хоть одна провинция,
// по убыванию числа провинций.
func (g *GameState) Economies() []Economy {
	by := make(map[int]*Economy)
	get := func(owner int) *Economy {
		if e, ok := by[owner]; ok {
			return e
		}
		e := &Economy{Player: g.Players[owner], Output: map[int]float64{},
			Populace: map[int]float64{}, Buildings: map[int]float64{}, Army: map[int]float64{}}
		e.Player.ID = owner
		by[owner] = e
		return e
	}

	for _, p := range g.Provinces {
		if p.Owner <= 0 {
			continue
		}
		e := get(p.Owner)
		e.Provinces++
		addInto(e.Output, p.Output, 1)
		addInto(e.Populace, g.ProvinceUpkeep(p), 1)
		for _, id := range p.Built {
			addInto(e.Buildings, g.BuildingUpkeep(id, p.Condition[id]), 1)
		}
	}

	for _, a := range g.Armies {
		e, ok := by[a.Owner]
		if !ok {
			continue // армия без провинций: игрок уже выбыл
		}
		for _, u := range a.Units {
			addInto(e.Army, g.UnitUpkeep[u.Type], float64(u.Size))
		}
	}

	if e, ok := by[g.Me]; ok && g.Me > 0 && len(g.Resources) > 0 {
		e.Actual = make(map[int]float64, len(g.Resources))
		for id, r := range g.Resources {
			e.Actual[id] = r.Daily
		}
	}

	out := make([]Economy, 0, len(by))
	for _, e := range by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provinces != out[j].Provinces {
			return out[i].Provinces > out[j].Provinces
		}
		return out[i].Player.ID < out[j].Player.ID
	})
	return out
}

// ProvinceUpkeep — потребление населения провинции в сутки.
func (g *GameState) ProvinceUpkeep(p Province) map[int]float64 {
	if len(g.Consumption) == 0 {
		return nil
	}
	factor := 1.0
	if f, ok := g.StateFactor[p.StateID]; ok {
		factor = f
	}
	out := make(map[int]float64, len(g.Consumption))
	for res, per := range g.Consumption {
		if v := math.Floor(p.Population*per) * factor; v > 0 {
			out[res] = v
		}
	}
	return out
}

// BuildingUpkeep — содержание здания в сутки при этом состоянии: своё,
// по доле от полного, и целиком — всех уровней, которые оно заменило.
func (g *GameState) BuildingUpkeep(id, condition int) map[int]float64 {
	u, ok := g.Upgrades[id]
	if !ok || len(u.DailyCosts) == 0 && u.Replaces == 0 {
		return nil
	}
	share := 1.0
	if u.MaxCondition > 0 {
		share = math.Min(float64(condition)/float64(u.MaxCondition), 1)
	}
	out := make(map[int]float64)
	addInto(out, u.DailyCosts, share)
	// Цепочка уровней конечна, но справочник чужой: обрываем её на всякий
	// случай, чтобы кольцо в нём не повесило страницу.
	for prev, hops := u.Replaces, 0; prev > 0 && hops < 32; hops++ {
		r, ok := g.Upgrades[prev]
		if !ok {
			break
		}
		addInto(out, r.DailyCosts, 1)
		prev = r.Replaces
	}
	return out
}

func addInto(dst, src map[int]float64, k float64) {
	for res, v := range src {
		dst[res] += v * k
	}
}

// numMap разбирает «ресурс → число» из сырого ответа. Игра шлёт такие
// таблицы объектами, иногда пустым массивом и с служебным «@c» —
// всё, что не пара «номер: число», пропускаем.
func numMap(raw json.RawMessage) map[int]float64 {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	out := make(map[int]float64, len(m))
	for k, v := range m {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		var f float64
		if json.Unmarshal(v, &f) == nil {
			out[id] = f
		}
	}
	return out
}

// entries раскладывает java-мапу или массив объектов в пары «номер →
// объект». Номер — ключ мапы или, если ключ не число, поле idField.
func entries(raw json.RawMessage, idField string) map[int]json.RawMessage {
	out := make(map[int]json.RawMessage)
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for i, v := range list {
			if id := fieldInt(v, idField); id >= 0 {
				out[id] = v
			} else {
				out[i] = v
			}
		}
		return out
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for k, v := range m {
		if id, err := strconv.Atoi(k); err == nil {
			out[id] = v
		} else if id := fieldInt(v, idField); id >= 0 {
			out[id] = v
		}
	}
	return out
}

func fieldInt(raw json.RawMessage, name string) int {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return -1
	}
	var n int
	if v, ok := m[name]; ok && json.Unmarshal(v, &n) == nil {
		return n
	}
	return -1
}

// resourceNames — названия ресурсов из справочника партии.
func resourceNames(raw json.RawMessage) map[int]string {
	out := make(map[int]string)
	for id, v := range entries(raw, "resourceID") {
		var e struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(v, &e) == nil && e.Name != "" {
			out[id] = e.Name
		}
	}
	return out
}

// stateFactors — множитель потребления по состоянию провинции. Нет
// поля — множителя нет, провинция потребляет как есть.
func stateFactors(raw json.RawMessage) map[int]float64 {
	out := make(map[int]float64)
	for id, v := range entries(raw, "id") {
		var e struct {
			Factor *float64 `json:"consumptionFactor"`
		}
		if json.Unmarshal(v, &e) == nil && e.Factor != nil {
			out[id] = *e.Factor
		}
	}
	return out
}
