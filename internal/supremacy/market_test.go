package supremacy

import (
	"encoding/json"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

// Кусок живого состояния (UltResourceState.asks): списки по номеру ресурса.
const asksSample = `[
 ["ultshared.UltAskList",[
   {"@c":"ultshared.UltOrder","buy":false,"amount":1010,"limit":13.7,"playerID":24,"resourceType":0,"orderID":3913},
   {"@c":"ultshared.UltOrder","buy":false,"amount":850,"limit":10.3,"playerID":0,"resourceType":0,"orderID":3960}]],
 ["ultshared.UltAskList",[]]
]`

func TestMarketAsks(t *testing.T) {
	var lists []json.RawMessage
	if err := json.Unmarshal([]byte(asksSample), &lists); err != nil {
		t.Fatal(err)
	}
	asks := marketAsks(lists)
	if len(asks) != 2 {
		t.Fatalf("ресурсов %d, ждали 2", len(asks))
	}
	if got := asks[0]; len(got) != 2 || got[0].OrderID != 3960 || got[0].Price != 10.3 || got[0].Amount != 850 {
		t.Fatalf("предложения зерна не по цене: %+v", got)
	}
	if _, ok := asks[1]; !ok {
		t.Fatal("ресурс без предложений пропал из карты")
	}
}

func TestMarketFee(t *testing.T) {
	for raw, want := range map[string]float64{`10`: 0.1, `"5"`: 0.05, ``: marketFeeDefault} {
		if got := marketFee(json.RawMessage(raw)); got != want {
			t.Errorf("marketFee(%s) = %v, ждали %v", raw, got, want)
		}
	}
}

func TestTradeCost(t *testing.T) {
	// 850 × 10.3 = 8755, комиссия 10% — 876 сверху.
	if got := TradeCost(850, 10.3, 0.1); got != 9631 {
		t.Fatalf("TradeCost = %d, ждали 9631", got)
	}
	n := affordUnits(9631, 10.3, 0.1)
	if n != 850 || TradeCost(n+1, 10.3, 0.1) <= 9631 {
		t.Fatalf("affordUnits = %d, ждали 850", n)
	}
}

func TestPlanMarket(t *testing.T) {
	now := time.Now()
	g := &GameState{
		Me:        7,
		MarketFee: 0.1,
		Resources: map[int]Resource{resMoney: {ID: resMoney, Amount: 100000, Measured: now}},
		Asks: map[int][]MarketOffer{
			0: {
				{OrderID: 1, Player: 7, Amount: 500, Price: 5},  // своё
				{OrderID: 2, Player: 3, Amount: 500, Price: 6},  // эмбарго
				{OrderID: 3, Player: 4, Amount: 100, Price: 8},  // берём целиком
				{OrderID: 4, Player: 5, Amount: 5000, Price: 9}, // берём на остаток
				{OrderID: 5, Player: 6, Amount: 5000, Price: 12},
			},
			1: {{OrderID: 6, Player: 4, Amount: 100, Price: 20}},
		},
		Relations: map[int]int{3: 0, 4: 1},
	}
	orders := []domain.MarketOrder{
		{ID: 10, Resource: 0, Budget: 5000, MaxPrice: 10},
		{ID: 11, Resource: 1, Budget: 5000, MaxPrice: 10},
	}
	plan := PlanMarket(g, orders, now)
	if len(plan.Buys) != 2 {
		t.Fatalf("покупок %d, ждали 2: %+v", len(plan.Buys), plan.Buys)
	}
	first, second := plan.Buys[0], plan.Buys[1]
	if first.Offer.OrderID != 3 || first.Amount != 100 || first.Cost != 880 {
		t.Fatalf("первая покупка %+v", first)
	}
	if second.Offer.OrderID != 4 || first.Cost+second.Cost > 5000 || second.Amount != affordUnits(5000-880, 9, 0.1) {
		t.Fatalf("вторая покупка %+v", second)
	}
	if plan.Notes[11] == "" {
		t.Fatal("по дорогому ресурсу нет объяснения")
	}
}

func TestPlanMarketCash(t *testing.T) {
	now := time.Now()
	g := &GameState{
		Me: 7, MarketFee: 0.1,
		Resources: map[int]Resource{resMoney: {ID: resMoney, Amount: 5, Measured: now}},
		Asks:      map[int][]MarketOffer{0: {{OrderID: 3, Player: 4, Amount: 100, Price: 8}}},
	}
	plan := PlanMarket(g, []domain.MarketOrder{{ID: 1, Resource: 0, Budget: 5000, MaxPrice: 10}}, now)
	if len(plan.Buys) != 0 || plan.Notes[1] != "не хватает денег" {
		t.Fatalf("без денег: %+v", plan)
	}
}
