package supremacy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"Vendetta_admin/internal/supremacy/heroes"
)

// HeroOffers спрашивает у сайта лестницу прокачки героев: во сколько
// осколков обходится каждая ступень. Это витрина магазина, а не состояние
// партии, — заходом в партию вызов не считается.
//
// Ответ приходит по всем героям сразу, включая тех, кого у нас нет:
// справочник тем и собирается, что игра рассказывает о них всем одинаково.
func (c *Client) HeroOffers(ctx context.Context) ([]heroes.Offer, error) {
	// userID идёт в параметрах, поэтому сессию берём заранее — как
	// и в MyGames.
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := c.call(ctx, "getOffers", []param{
		{"forHeroes", "1"},
		{"userID", sess.userID},
		{"locale", c.lang},
	})
	if err != nil {
		return nil, err
	}
	return parseHeroOffers(raw)
}

// parseHeroOffers разбирает витрину. Числа игра отдаёт строками («290.0»),
// и цена нам нужна целой: осколки дробными не бывают.
func parseHeroOffers(raw json.RawMessage) ([]heroes.Offer, error) {
	var list []struct {
		Name  string            `json:"name"`
		Items []int             `json:"ii"`
		Price map[string]string `json:"pc"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("разбор витрины героев: %w", err)
	}

	out := make([]heroes.Offer, 0, len(list))
	for _, o := range list {
		if len(o.Price) == 0 {
			continue
		}
		shards := make(map[int]int, len(o.Price))
		for item, price := range o.Price {
			id, err := strconv.Atoi(item)
			if err != nil {
				continue
			}
			amount, err := strconv.ParseFloat(price, 64)
			if err != nil {
				continue
			}
			shards[id] = int(amount)
		}
		if len(shards) == 0 {
			continue
		}
		out = append(out, heroes.Offer{Name: o.Name, Items: o.Items, Shards: shards})
	}
	return out, nil
}
