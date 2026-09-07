package supremacy

import (
	"context"
	"time"
)

// Состояние партии — самое дорогое, что мы берём у игры: полтора мегабайта
// и секунды ожидания на каждый запрос. Спрашивают его все — страницы, сборщик
// коалиций, автопилот, — и спрашивают об одних и тех же партиях. Поэтому копия
// одна на всех: кто первым привёз, тот и наполнил, остальные читают готовое.
// Игра при этом видит один запрос на партию за срок годности, сколько бы людей
// её ни смотрело.

// stateFresh — сколько копия считается свежей в обычной партии. Полтора часа
// — это полтора игровых часа: в скоростной партии игровое время идёт быстрее,
// и держать копию столько же было бы враньём, поэтому срок делится на скорость
// партии. У x4 выходит двадцать две минуты, у x10 — девять.
const stateFresh = 90 * time.Minute

// stateFreshUnknown — срок для партии, скорости которой мы ещё не знаем.
// Берём самый короткий: лишний поход дешевле, чем вчерашняя карта, выданная
// за сегодняшнюю.
const stateFreshUnknown = 9 * time.Minute

// rootStateFresh — рут смотрит свежее остальных: он чинит и отлаживает,
// и данные часовой давности ему бесполезны. Дольше общего срока это право
// не действует — в быстрой партии ему достаётся тот же срок, что и всем.
const rootStateFresh = 15 * time.Minute

// stateCacheMax — сколько партий держим разом. Полтора мегабайта на партию,
// и сборщик коалиций приносит по пять партий каждые десять минут: без потолка
// кэш растёт быстрее, чем стареет. Выбрасываем ту, к которой дольше всех
// не обращались, — партия, которую смотрят люди, из кэша так не вылетит.
const stateCacheMax = 24

// stateEntry — ячейка под одну партию. Поля читают и пишут под замком
// клиента, busy пропускает к игровому серверу по одному.
type stateEntry struct {
	busy  chan struct{}
	state *GameState
	// at — когда копию сняли, used — когда её последний раз спрашивали.
	// Первое решает, свежая ли она, второе — кого выбрасывать.
	at   time.Time
	used time.Time
}

// StateFresh — сколько копия состояния этой партии считается свежей.
// Знать это нужно и странице: она показывает, когда можно будет взять новую.
func (c *Client) StateFresh(gameID string, root bool) time.Duration {
	c.mu.Lock()
	speed := c.speeds[gameID]
	c.mu.Unlock()

	ttl := stateFreshUnknown
	if speed >= 1 {
		ttl = time.Duration(float64(stateFresh) / speed)
	}
	if root && rootStateFresh < ttl {
		return rootStateFresh
	}
	return ttl
}

// StateFor отдаёт состояние партии: из общего кэша, если копия ещё свежая,
// иначе с игрового сервера. Второй ответ — когда копию сняли; страница
// показывает это время, чтобы старое не сошло за новое.
//
// ours решает, как заходить: в свою партию игроком, в чужую наблюдателем.
// Заход игроком игра засчитывает как вход, поэтому свежая копия ценна вдвойне
// — она этот вход бережёт.
func (c *Client) StateFor(ctx context.Context, gameID string, ours, root bool) (*GameState, time.Time, error) {
	e := c.stateEntry(gameID)
	maxAge := c.StateFresh(gameID, root)

	// По одному походу на партию: двое, открывшие её разом, ждут один
	// ответ, а игра видит один запрос. Ждём под ctx, а не на мьютексе:
	// страницу, которая уже никому не нужна, держать незачем.
	select {
	case e.busy <- struct{}{}:
		defer func() { <-e.busy }()
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}

	if state, at, ok := c.freshState(gameID, maxAge); ok {
		return state, at, nil
	}

	var (
		state *GameState
		err   error
	)
	if ours {
		state, err = c.GameState(ctx, gameID)
	} else {
		state, err = c.ObserveGame(ctx, gameID)
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	// Копию положил gameState — единственное место, где состояние приезжает
	// с сервера. Оттуда её и берём: первый спрашивающий должен получить
	// ровно то же, что и все следующие.
	if state, at, ok := c.freshState(gameID, maxAge); ok {
		return state, at, nil
	}
	return state, time.Now(), nil
}

// freshState отдаёт копию, если она не старше maxAge. Состояние отдаётся
// общим указателем, а не копией: полтора мегабайта на каждого смотрящего —
// слишком дорого, а читают его все только на чтение, как и очертания карты.
func (c *Client) freshState(gameID string, maxAge time.Duration) (*GameState, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.states[gameID]
	if !ok || e.state == nil || time.Since(e.at) > maxAge {
		return nil, time.Time{}, false
	}
	e.used = time.Now()
	return e.state, e.at, true
}

// cacheState кладёт свежепривезённую копию. Зовётся из единственного места,
// где состояние приходит с игрового сервера, поэтому кэш наполняют все разом:
// и страница, и сборщик коалиций, и автопилот.
func (c *Client) cacheState(gameID string, state *GameState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.stateEntryLocked(gameID)
	now := time.Now()
	e.state, e.at, e.used = state, now, now
	c.evictStatesLocked()
}

func (c *Client) stateEntry(gameID string) *stateEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateEntryLocked(gameID)
}

func (c *Client) stateEntryLocked(gameID string) *stateEntry {
	if c.states == nil {
		c.states = make(map[string]*stateEntry)
	}
	if e, ok := c.states[gameID]; ok {
		return e
	}
	e := &stateEntry{busy: make(chan struct{}, 1), used: time.Now()}
	c.states[gameID] = e
	return e
}

// evictStatesLocked держит кэш в размере: сначала уходят протухшие по самому
// долгому сроку — они бесполезны любой партии, — а если и после этого партий
// больше потолка, то та, к которой дольше всех не обращались.
func (c *Client) evictStatesLocked() {
	for id, e := range c.states {
		if e.state != nil && time.Since(e.at) > stateFresh {
			delete(c.states, id)
		}
	}
	for len(c.states) > stateCacheMax {
		var (
			oldest   string
			oldestAt time.Time
		)
		for id, e := range c.states {
			if oldest == "" || e.used.Before(oldestAt) {
				oldest, oldestAt = id, e.used
			}
		}
		delete(c.states, oldest)
	}
}

// noteSpeeds запоминает скорость увиденных партий: по ней считается срок
// годности копии. Скорость у партии не меняется, поэтому и срока хранения
// у этой памяти нет — float на партию не жалко.
func (c *Client) noteSpeeds(games ...Game) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.speeds == nil {
		c.speeds = make(map[string]float64)
	}
	for _, g := range games {
		if g.GameID != "" {
			c.speeds[g.GameID] = g.Speed()
		}
	}
}
