package supremacy

// Биржа партии: покупка ресурса по заявке «не дороже такой-то цены, пока
// не уйдёт такая-то сумма». Своей заявки в стакан мы не ставим — берём
// чужие предложения продажи, как это делает игрок, нажав «Купить» у
// предложения в окне биржи. Приказ тот же, что шлёт клиент игры
// (AcceptOrderController.onAcceptTrade):
//
//	{"@c":"ultshared.action.UltOrderAction","cancel":false,"order":
//	  {"@c":"ultshared.UltOrder","orderID":3960,"buy":true,"amount":850,
//	   "limit":10.3,"resourceType":0,"playerID":<мы>,…}}
//
// orderID — номер чужого предложения, buy — противоположное ему, limit —
// его цена. Платим сверх цены комиссию биржи (marketFeePercentage мода):
// round(amount × limit) + ceil(round(amount × limit) × комиссия).
//
// Воркер ходит в партию раз в несколько минут (свой срок у каждой заявки)
// и смотрит только базу между заходами. Потраченная сумма заявку закрывает.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
)

const (
	// marketTick — как часто смотреть в базу, не пора ли за какой-то заявкой.
	marketTick = time.Minute

	// marketFeeDefault — комиссия, если мод о ней не сказал. Такая стоит
	// во всех партиях, что мы видели; лучше переплатить в расчёте, чем
	// получить отказ за нехватку денег.
	marketFeeDefault = 0.1

	// marketConfirmShare — какую долю купленного должен показать запас
	// после захода, чтобы покупку считать прошедшей. Не единица: запас
	// тем временем тратится и прирастает.
	marketConfirmShare = 0.8
)

// MarketOffer — предложение продажи на бирже.
type MarketOffer struct {
	OrderID int64
	Player  int
	Amount  int
	Price   float64
}

// marketAsks разбирает предложения продажи: список по номеру ресурса,
// каждый — ["ultshared.UltAskList",[заявка, …]]. Сломанный элемент
// пропускаем — ресурс тогда просто без предложений.
func marketAsks(lists []json.RawMessage) map[int][]MarketOffer {
	if len(lists) == 0 {
		return nil
	}
	out := make(map[int][]MarketOffer, len(lists))
	for res, raw := range lists {
		out[res] = nil
		entries, ok := productionEntries(raw)
		if !ok {
			continue
		}
		for _, e := range entries {
			var o struct {
				Buy      bool    `json:"buy"`
				Amount   float64 `json:"amount"`
				Limit    float64 `json:"limit"`
				Player   int     `json:"playerID"`
				Resource int     `json:"resourceType"`
				OrderID  int64   `json:"orderID"`
			}
			if json.Unmarshal(e, &o) != nil || o.Buy || o.Amount < 1 || o.Limit <= 0 {
				continue
			}
			out[res] = append(out[res], MarketOffer{
				OrderID: o.OrderID, Player: o.Player, Amount: int(o.Amount), Price: o.Limit,
			})
		}
		sort.SliceStable(out[res], func(i, j int) bool { return out[res][i].Price < out[res][j].Price })
	}
	return out
}

// marketFee — комиссия биржи долей из процентов мода, числом или строкой.
func marketFee(raw json.RawMessage) float64 {
	var f flexFloat
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil || f < 0 {
		return marketFeeDefault
	}
	return float64(f) / 100
}

// flexFloat — число, которое игра шлёт то числом, то строкой.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	var v float64
	if _, err := fmt.Sscan(s, &v); err != nil {
		return err
	}
	*f = flexFloat(v)
	return nil
}

// TradeCost — сколько денег уйдёт за amount единиц по цене price вместе
// с комиссией: так считает клиент игры (UltOrder.getTotalPriceWithFee).
func TradeCost(amount int, price, fee float64) int {
	total := math.Round(float64(amount) * price)
	return int(total + math.Ceil(total*fee))
}

// affordUnits — сколько единиц по цене price можно взять на money денег.
func affordUnits(money int, price, fee float64) int {
	if money <= 0 || price <= 0 {
		return 0
	}
	n := int(float64(money) / (price * (1 + fee)))
	for n > 0 && TradeCost(n, price, fee) > money {
		n--
	}
	return n
}

// Tradeable — ресурсы, которыми торгует биржа партии, по номеру.
func (g *GameState) Tradeable() []int {
	out := make([]int, 0, len(g.Asks))
	for id := range g.Asks {
		if id != resMoney && g.ResourceNames[id] != "" {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// MarketBuy — одна покупка: у какого предложения и сколько.
type MarketBuy struct {
	OrderID  int64 // наша заявка в базе
	Resource int
	Offer    MarketOffer
	Amount   int
	Cost     int
}

// MarketRun — итог захода по одной заявке.
type MarketRun struct {
	ID     int64
	Spent  int
	Bought int
	Result string
	// Next — когда заходить снова; нулевое — заявку закрываем.
	Next time.Time
}

// MarketPlan — что купить в этот заход и что сказать по каждой заявке,
// если покупать нечего.
type MarketPlan struct {
	Buys []MarketBuy
	// Notes — почему по заявке ничего не берём, по её номеру.
	Notes map[int64]string
}

// PlanMarket решает, что купить, по состоянию партии и открытым заявкам.
// В сеть не ходит. Деньги общие: каждая покупка уменьшает запас, и
// следующей заявке достаётся остаток.
func PlanMarket(g *GameState, orders []domain.MarketOrder, now time.Time) MarketPlan {
	plan := MarketPlan{Notes: map[int64]string{}}
	cash := 0
	if r, ok := g.Resources[resMoney]; ok {
		cash = int(r.AmountAt(now))
	}
	for _, o := range orders {
		offers, traded := g.Asks[o.Resource]
		if !traded {
			plan.Notes[o.ID] = "этим ресурсом биржа партии не торгует"
			continue
		}
		left := o.Left()
		cheap := 0
		for _, offer := range offers {
			if offer.Price > o.MaxPrice {
				break
			}
			// Своё предложение не покупаем, а с тем, с кем у нас эмбарго
			// или война, игра торговать не даст (canTrade у клиента).
			if offer.Player == g.Me {
				continue
			}
			if rel, ok := g.Relations[offer.Player]; ok && offer.Player > 0 && rel <= 0 {
				continue
			}
			cheap++
			n := min(offer.Amount, affordUnits(min(left, cash), offer.Price, g.MarketFee))
			if n < 1 {
				break
			}
			cost := TradeCost(n, offer.Price, g.MarketFee)
			plan.Buys = append(plan.Buys, MarketBuy{
				OrderID: o.ID, Resource: o.Resource, Offer: offer, Amount: n, Cost: cost,
			})
			left -= cost
			cash -= cost
		}
		switch {
		case cheap == 0:
			best := "предложений нет"
			if len(offers) > 0 {
				best = fmt.Sprintf("дешевле всего %.1f", offers[0].Price)
			}
			plan.Notes[o.ID] = fmt.Sprintf("нет предложений по %.1f и ниже (%s)", o.MaxPrice, best)
		case left == o.Left():
			plan.Notes[o.ID] = "не хватает денег"
		}
	}
	return plan
}

// RunMarket — один заход в партию ради заявок на покупку: смотрим биржу,
// берём подходящие предложения и перечитываем запасы, чтобы убедиться,
// что ресурс пришёл. Игра может и не провести покупку: предложение успели
// купить другие.
func (c *Client) RunMarket(ctx context.Context, gameID string, orders []domain.MarketOrder, now time.Time) ([]MarketRun, error) {
	acc, state, err := c.enter(ctx, gameID)
	if err != nil {
		return nil, err
	}

	if state.Ended {
		runs := make([]MarketRun, len(orders))
		for i, o := range orders {
			runs[i] = MarketRun{ID: o.ID, Result: "партия кончилась — заявка закрыта"}
		}
		return runs, nil
	}

	plan := PlanMarket(state, orders, now)
	type sent struct{ spent, bought int }
	done := map[int64]*sent{}
	failed := map[int64][]string{}

	reqID := 3
	for _, b := range plan.Buys {
		var res json.RawMessage
		err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", state.Me,
			map[string]any{
				"actions": []any{map[string]any{
					"requestID": fmt.Sprintf("actionReq-%d", reqID),
					"@c":        "ultshared.action.UltOrderAction",
					"cancel":    false,
					"order": map[string]any{
						"@c":           "ultshared.UltOrder",
						"orderID":      b.Offer.OrderID,
						"buy":          true,
						"amount":       b.Amount,
						"limit":        b.Offer.Price,
						"resourceType": b.Resource,
						"playerID":     state.Me,
						"totalPrice":   0,
						"embargo":      false,
						"isOwner":      false,
						"buyable":      false,
					},
				}},
				"lastCallDuration": 0,
			}, &res)
		reqID++
		if err != nil {
			failed[b.OrderID] = append(failed[b.OrderID], err.Error())
			c.log.Warn("биржа: покупка не принята", "gameID", gameID, "ресурс", b.Resource,
				"предложение", b.Offer.OrderID, "err", err)
			continue
		}
		s := done[b.OrderID]
		if s == nil {
			s = &sent{}
			done[b.OrderID] = s
		}
		s.spent += b.Cost
		s.bought += b.Amount
		c.log.Info("биржа: покупка", "gameID", gameID, "ресурс", b.Resource,
			"сколько", b.Amount, "цена", b.Offer.Price, "денег", b.Cost)
	}

	// Проверка: пришёл ли ресурс. Прирост и расход за эти секунды
	// учитывает AmountAt, поэтому сравниваем запасы на один и тот же миг.
	var after *GameState
	if len(done) > 0 {
		after, err = c.gameState(ctx, acc, gameID, reqID, state.Me)
		if err != nil {
			c.log.Warn("биржа: запасы после покупки", "gameID", gameID, "err", err)
		}
	}
	checkAt := time.Now()

	runs := make([]MarketRun, 0, len(orders))
	for _, o := range orders {
		run := MarketRun{ID: o.ID}
		var parts []string
		if s := done[o.ID]; s != nil {
			confirmed := true
			if after != nil {
				gained := after.Resources[o.Resource].AmountAt(checkAt) - state.Resources[o.Resource].AmountAt(checkAt)
				confirmed = gained >= float64(s.bought)*marketConfirmShare
			}
			if confirmed {
				run.Spent, run.Bought = s.spent, s.bought
				msg := fmt.Sprintf("купили %d за %d", s.bought, s.spent)
				if after == nil {
					msg += " (запасы не перечитали)"
				}
				parts = append(parts, msg)
			} else {
				parts = append(parts, "игра не провела покупку — предложение успели забрать")
			}
		}
		if f := failed[o.ID]; len(f) > 0 {
			parts = append(parts, "отказ игры: "+strings.Join(f, "; "))
		}
		if note := plan.Notes[o.ID]; note != "" {
			parts = append(parts, note)
		}
		if len(parts) == 0 {
			parts = append(parts, "покупать нечего")
		}

		// Хватит ли остатка хотя бы на единицу по своей цене. Нет —
		// сумма потрачена, и заходить за этой заявкой больше незачем.
		o.Spent += run.Spent
		if affordUnits(o.Left(), o.MaxPrice, state.MarketFee) < 1 {
			parts = append(parts, "сумма потрачена — заявка закрыта")
		} else {
			run.Next = now.Add(marketEvery(o))
		}
		run.Result = upperFirst(strings.Join(parts, "; "))
		runs = append(runs, run)
	}
	return runs, nil
}

// marketEvery — срок заявки до следующего захода.
func marketEvery(o domain.MarketOrder) time.Duration {
	if o.Every <= 0 {
		return domain.MarketEveryDefault * time.Minute
	}
	return time.Duration(o.Every) * time.Minute
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	}
	return string(r)
}

// MarketStore — где лежат заявки. В бою это репозиторий.
type MarketStore interface {
	Due(ctx context.Context, now time.Time) ([]domain.MarketOrder, error)
	Open(ctx context.Context, gameID string) ([]domain.MarketOrder, error)
	Mark(ctx context.Context, id int64, at time.Time, result string, spent, bought int, next time.Time) error
}

type marketRunner interface {
	RunMarket(ctx context.Context, gameID string, orders []domain.MarketOrder, now time.Time) ([]MarketRun, error)
}

// MarketWatcher ходит за заявками на покупку. Раз в минуту он спрашивает
// только базу, а в партию идёт, когда у какой-то заявки подошёл срок; за
// один заход разбираются все открытые заявки партии.
type MarketWatcher struct {
	client marketRunner
	store  MarketStore
	log    *slog.Logger
	now    func() time.Time
	// jitter — сдвиг следующего захода: ровный ритм входов — подпись робота.
	jitter func(every time.Duration) time.Duration
}

func NewMarketWatcher(c marketRunner, store MarketStore, log *slog.Logger) *MarketWatcher {
	return &MarketWatcher{
		client: c, store: store, log: log, now: time.Now,
		jitter: func(every time.Duration) time.Duration { return rand.N(every/10 + time.Second) },
	}
}

func (m *MarketWatcher) Run(ctx context.Context) {
	m.log.Info("заявки биржи запущены", "проверка", marketTick)
	ticker := time.NewTicker(marketTick)
	defer ticker.Stop()
	for {
		if err := m.tick(ctx); err != nil && ctx.Err() == nil {
			m.log.Error("обход заявок биржи", "err", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (m *MarketWatcher) tick(ctx context.Context) error {
	due, err := m.store.Due(ctx, m.now())
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, o := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if seen[o.GameID] {
			continue
		}
		seen[o.GameID] = true
		if err := m.visit(ctx, o.GameID); err != nil {
			m.log.Error("заявки биржи", "gameID", o.GameID, "err", err)
		}
	}
	return nil
}

// visit разбирает все открытые заявки партии одним заходом: раз уж
// зашли, смотрим биржу и для тех, чей срок ещё не подошёл.
func (m *MarketWatcher) visit(ctx context.Context, gameID string) error {
	orders, err := m.store.Open(ctx, gameID)
	if err != nil || len(orders) == 0 {
		return err
	}
	now := m.now()
	runs, err := m.client.RunMarket(ctx, gameID, orders, now)
	if err != nil {
		// Не зашли — заявки ждут своего обычного срока.
		runs = make([]MarketRun, len(orders))
		for i, o := range orders {
			runs[i] = MarketRun{ID: o.ID, Result: "Не вышло зайти в партию: " + err.Error(),
				Next: now.Add(marketEvery(o))}
		}
	}
	for _, run := range runs {
		if !run.Next.IsZero() {
			run.Next = run.Next.Add(m.jitter(run.Next.Sub(now)))
		}
		m.log.Info("заявка биржи", "gameID", gameID, "заявка", run.ID, "итог", run.Result, "следующий заход", run.Next)
		if err := m.store.Mark(ctx, run.ID, now, run.Result, run.Spent, run.Bought, run.Next); err != nil {
			m.log.Error("запись итога заявки биржи", "gameID", gameID, "err", err)
		}
	}
	return nil
}
